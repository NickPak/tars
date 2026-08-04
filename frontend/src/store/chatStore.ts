import { create } from "zustand";
import { agentApi, subscribeAgentEvents } from "../services/agentApi";
import type { ChatMessage, Conversation } from "../types";

export interface ConversationMeta {
  id: string;
  title: string;
  updatedAt: number;
}

interface ChatState {
  conversations: ConversationMeta[];
  activeId: string | null;
  messages: ChatMessage[];
  isStreaming: boolean;
  /** 后端调用失败信息（联调阶段用于提示，可关闭） */
  backendError: string | null;

  /** 加载会话列表并订阅流式事件，返回清理函数 */
  init: () => () => void;
  /** 切换到空白新会话（首次发送时才真正创建） */
  newConversation: () => void;
  selectConversation: (id: string) => Promise<void>;
  deleteConversation: (id: string) => Promise<void>;
  renameConversation: (id: string, title: string) => Promise<void>;
  send: (text: string) => Promise<void>;
  cancel: () => Promise<void>;
  deleteMessage: (messageId: string) => Promise<void>;
  dismissError: () => void;
}

function toMeta(c: Conversation): ConversationMeta {
  return { id: c.id, title: c.title, updatedAt: c.updatedAt };
}

function errText(e: unknown): string {
  return e instanceof Error ? e.message : String(e);
}

/** 将错误标注到最后一条 assistant 消息上 */
function markLastAssistantError(
  messages: ChatMessage[],
  error: string,
): ChatMessage[] {
  const next = [...messages];
  const last = next[next.length - 1];
  if (last && last.role === "assistant") {
    next[next.length - 1] = { ...last, error };
  }
  return next;
}

export const useChatStore = create<ChatState>((set, get) => ({
  conversations: [],
  activeId: null,
  messages: [],
  isStreaming: false,
  backendError: null,

  init: () => {
    agentApi
      .listConversations()
      .then((list) => {
        const convs = (list ?? []).map(toMeta);
        convs.sort((a, b) => b.updatedAt - a.updatedAt);
        set({ conversations: convs });
      })
      .catch((e) => set({ backendError: errText(e) }));

    return subscribeAgentEvents({
      onChunk: ({ conversationId, chunk }) => {
        if (!get().isStreaming || conversationId !== get().activeId) return;
        set((s) => {
          const messages = [...s.messages];
          const last = messages[messages.length - 1];
          if (!last || last.role !== "assistant") return {};
          messages[messages.length - 1] = {
            ...last,
            content: last.content + chunk,
          };
          return { messages };
        });
      },
      onDone: ({ conversationId, usage, elapsedMs }) => {
        if (conversationId !== get().activeId) return;
        set((s) => {
          const messages = [...s.messages];
          const last = messages[messages.length - 1];
          if (last && last.role === "assistant") {
            messages[messages.length - 1] = { ...last, usage, elapsedMs };
          }
          return { messages, isStreaming: false };
        });
      },
      onError: (err) => {
        if (err.conversationId !== get().activeId) return;
        set((s) => ({
          messages: markLastAssistantError(s.messages, err.error),
          isStreaming: false,
        }));
      },
      onConversationRenamed: ({ conversationId, title }) => {
        set((s) => ({
          conversations: s.conversations.map((c) =>
            c.id === conversationId ? { ...c, title } : c,
          ),
        }));
      },
      onReasoning: ({ conversationId, content }) => {
        if (!get().isStreaming || conversationId !== get().activeId) return;
        set((s) => {
          const messages = [...s.messages];
          const last = messages[messages.length - 1];
          if (!last || last.role !== "assistant") return {};
          // reasoning 可能多轮（每次工具调用前模型都会思考），追加到已有 reasoning 后
          messages[messages.length - 1] = {
            ...last,
            reasoning: (last.reasoning ?? "") + content,
          };
          return { messages };
        });
      },
      onTool: ({ conversationId, toolCallId, toolName, args }) => {
        if (conversationId !== get().activeId) return;
        set((s) => {
          const messages = [...s.messages];
          const last = messages[messages.length - 1];
          if (!last || last.role !== "assistant") return {};
          const toolCalls = [...(last.toolCalls ?? []), { id: toolCallId, name: toolName, args }];
          messages[messages.length - 1] = { ...last, toolCalls };
          return { messages };
        });
      },
      onToolResult: ({ conversationId, toolCallId, output }) => {
        if (conversationId !== get().activeId) return;
        set((s) => {
          const messages = [...s.messages];
          const last = messages[messages.length - 1];
          if (!last || last.role !== "assistant" || !last.toolCalls) return {};
          const toolCalls = last.toolCalls.map((tc) =>
            tc.id === toolCallId ? { ...tc, output } : tc,
          );
          messages[messages.length - 1] = { ...last, toolCalls };
          return { messages };
        });
      },
    });
  },

  newConversation: () => {
    if (get().isStreaming) return;
    set({ activeId: null, messages: [] });
  },

  selectConversation: async (id) => {
    if (get().isStreaming || id === get().activeId) return;
    set({ activeId: id, messages: [] });
    try {
      const conv = await agentApi.getConversation(id);
      set({ messages: conv.messages ?? [] });
    } catch (e) {
      set({ backendError: errText(e) });
    }
  },

  deleteConversation: async (id) => {
    try {
      await agentApi.deleteConversation(id);
      set((s) => ({
        conversations: s.conversations.filter((c) => c.id !== id),
        ...(s.activeId === id ? { activeId: null, messages: [] } : {}),
      }));
    } catch (e) {
      set({ backendError: errText(e) });
    }
  },

  renameConversation: async (id, title) => {
    try {
      await agentApi.renameConversation(id, title);
      set((s) => ({
        conversations: s.conversations.map((c) =>
          c.id === id ? { ...c, title } : c,
        ),
      }));
    } catch (e) {
      set({ backendError: errText(e) });
    }
  },

  send: async (text) => {
    const content = text.trim();
    if (!content || get().isStreaming) return;

    // 新会话：首次发送时才请求后端创建
    let convId = get().activeId;
    if (!convId) {
      try {
        const conv = await agentApi.createConversation();
        convId = conv.id;
        set((s) => ({
          conversations: [toMeta(conv), ...s.conversations],
          activeId: convId,
        }));
      } catch (e) {
        set({ backendError: errText(e) });
        return;
      }
    }

    const now = Date.now();
    const userMsg: ChatMessage = {
      id: crypto.randomUUID(),
      role: "user",
      content,
      createdAt: now,
    };
    // 助手占位消息，内容由 "agent:chunk" 事件增量填充
    const assistantMsg: ChatMessage = {
      id: crypto.randomUUID(),
      role: "assistant",
      content: "",
      createdAt: now,
    };
    set((s) => ({
      messages: [...s.messages, userMsg, assistantMsg],
      isStreaming: true,
    }));

    try {
      await agentApi.sendMessage(convId, content);
    } catch (e) {
      set((s) => ({
        messages: markLastAssistantError(s.messages, errText(e)),
        isStreaming: false,
      }));
    }
  },

  cancel: async () => {
    const { activeId, isStreaming } = get();
    if (!activeId || !isStreaming) return;
    set({ isStreaming: false });
    try {
      await agentApi.cancelMessage(activeId);
    } catch (e) {
      set({ backendError: errText(e) });
    }
  },

  deleteMessage: async (messageId) => {
    const { activeId, messages } = get();
    if (!activeId) return;

    // 找到目标消息索引，删除它及其后所有消息
    const idx = messages.findIndex((m) => m.id === messageId);
    if (idx === -1) return;

    // 保存原始消息以便回滚
    const snapshot = messages;

    try {
      // 先调用后端删除（非乐观更新，避免回滚复杂度）
      await agentApi.deleteMessage(activeId, messageId);
      // 后端成功后才更新前端
      set({ messages: messages.slice(0, idx) });
    } catch (e) {
      // 删除失败，恢复原始消息列表
      set({ messages: snapshot, backendError: errText(e) });
    }
  },

  dismissError: () => set({ backendError: null }),
}));
