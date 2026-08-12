import { create } from "zustand";
import { agentApi, subscribeAgentEvents } from "../services/agentApi";
import type { ChatMessage, Session, ModelInfo, SessionStats, WorkspaceInfo } from "../types";

export interface SessionMeta {
  id: string;
  title: string;
  updatedAt: number;
}

interface ChatState {
  sessions: SessionMeta[];
  activeId: string | null;
  messages: ChatMessage[];
  isStreaming: boolean;
  /** 后端调用失败信息（联调阶段用于提示，可关闭） */
  backendError: string | null;
  /** 当前会话的工作区信息 */
  workspace: WorkspaceInfo | null;
  /** 当前会话的聚合统计（状态栏数据）；null 表示新会话/无数据 */
  stats: SessionStats | null;
  /** 当前激活模型信息（TopicBar 模型选择器展示）；model:changed 事件刷新 */
  model: ModelInfo | null;
  /** 全部已配置模型条目（模型切换下拉用） */
  models: ModelInfo[];

  /** 加载会话列表并订阅流式事件，返回清理函数 */
  init: () => () => void;
  /** 切换到空白新会话（首次发送时才真正创建） */
  newSession: () => void;
  selectSession: (id: string) => Promise<void>;
  deleteSession: (id: string) => Promise<void>;
  renameSession: (id: string, title: string) => Promise<void>;
  send: (text: string) => Promise<void>;
  cancel: () => Promise<void>;
  /** 重试生成最后一条 assistant 回复（先回撤已渲染内容，再重新流式） */
  retry: () => Promise<void>;
  deleteMessage: (messageId: string) => Promise<void>;
  dismissError: () => void;
  /** 设置后端错误横幅（供聊天域之外的组件复用错误提示通道） */
  setBackendError: (msg: string) => void;

  /** 弹出目录选择对话框并设置工作区 */
  pickAndSetWorkspace: () => Promise<void>;
  /** 重置为默认工作区 */
  resetWorkspace: () => Promise<void>;
  /** 拉取当前会话的聚合统计（done/error/切换会话后调用） */
  refreshStats: () => Promise<void>;
  /** 重新拉取模型信息（设置界面保存配置后调用，TopicBar/状态栏随之更新） */
  refreshModelInfo: () => Promise<void>;
  /** 重新拉取模型条目列表 */
  refreshModels: () => Promise<void>;
  /** 切换当前使用的模型（后端持久化并广播 model:changed） */
  setActiveModel: (id: string) => Promise<void>;
}

function toMeta(c: Session): SessionMeta {
  return { id: c.id, title: c.title, updatedAt: c.updatedAt };
}

function errText(e: unknown): string {
  return e instanceof Error ? e.message : String(e);
}

/** 将错误标注到最后一条 assistant 消息上 */
function markLastAssistantError(
  messages: ChatMessage[],
  error: string,
  errorKind?: "timeout" | "error",
): ChatMessage[] {
  const next = [...messages];
  const last = next[next.length - 1];
  if (last && last.role === "assistant") {
    next[next.length - 1] = { ...last, error, errorKind };
  }
  return next;
}

export const useChatStore = create<ChatState>((set, get) => ({
  sessions: [],
  activeId: null,
  messages: [],
  isStreaming: false,
  backendError: null,
  workspace: null,
  stats: null,
  model: null,
  models: [],

  init: () => {
    agentApi
      .listSessions()
      .then((list) => {
        const sessions = (list ?? []).map(toMeta);
        sessions.sort((a, b) => b.updatedAt - a.updatedAt);
        set({ sessions });
      })
      .catch((e) => set({ backendError: errText(e) }));

    // 加载当前模型配置与模型条目列表（应用级，不随会话变化）
    void get().refreshModelInfo();
    void get().refreshModels();

    return subscribeAgentEvents({
      onChunk: ({ sessionId, chunk }) => {
        if (!get().isStreaming || sessionId !== get().activeId) return;
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
      onDone: ({ sessionId, usage, elapsedMs }) => {
        if (sessionId !== get().activeId) return;
        set((s) => {
          const messages = [...s.messages];
          const last = messages[messages.length - 1];
          if (last && last.role === "assistant") {
            messages[messages.length - 1] = { ...last, usage, elapsedMs };
          }
          return { messages, isStreaming: false };
        });
        void get().refreshStats();
      },
      onError: (err) => {
        if (err.sessionId !== get().activeId) return;
        set((s) => ({
          messages: markLastAssistantError(s.messages, err.error, err.kind),
          isStreaming: false,
        }));
        void get().refreshStats();
      },
      onSessionRenamed: ({ sessionId, title }) => {
        set((s) => ({
          sessions: s.sessions.map((c) =>
            c.id === sessionId ? { ...c, title } : c,
          ),
        }));
      },
      onReasoning: ({ sessionId, content }) => {
        if (!get().isStreaming || sessionId !== get().activeId) return;
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
      onTool: ({ sessionId, toolCallId, toolName, args }) => {
        if (sessionId !== get().activeId) return;
        set((s) => {
          const messages = [...s.messages];
          const last = messages[messages.length - 1];
          if (!last || last.role !== "assistant") return {};
          const toolCalls = [...(last.toolCalls ?? []), { id: toolCallId, name: toolName, args }];
          messages[messages.length - 1] = { ...last, toolCalls };
          return { messages };
        });
      },
      onToolResult: ({ sessionId, toolCallId, output }) => {
        if (sessionId !== get().activeId) return;
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
      onWorkspaceChanged: ({ sessionId, path, isCustom }) => {
        if (sessionId !== get().activeId) return;
        const name = path ? path.split(/[/\\]/).pop() || path : "";
        set({ workspace: { path, isCustom, name } });
      },
      onModelChanged: ({ model }) => {
        set({ model });
        // 模型列表的 active 标记与状态栏统计随模型切换更新
        void get().refreshModels();
        void get().refreshStats();
      },
    });
  },

  newSession: () => {
    if (get().isStreaming) return;
    set({ activeId: null, messages: [], workspace: null, stats: null });
  },

  selectSession: async (id) => {
    if (get().isStreaming || id === get().activeId) return;
    set({ activeId: id, messages: [], workspace: null, stats: null });
    try {
      const sess = await agentApi.getSession(id);
      set({ messages: sess.messages ?? [] });
    } catch (e) {
      set({ backendError: errText(e) });
    }
    // 加载工作区信息
    try {
      const ws = await agentApi.getWorkspaceInfo(id);
      set({ workspace: ws });
    } catch {
      // 静默忽略，不影响会话加载
    }
    void get().refreshStats();
  },

  deleteSession: async (id) => {
    try {
      await agentApi.deleteSession(id);
      set((s) => ({
        sessions: s.sessions.filter((c) => c.id !== id),
        ...(s.activeId === id ? { activeId: null, messages: [] } : {}),
      }));
    } catch (e) {
      set({ backendError: errText(e) });
    }
  },

  renameSession: async (id, title) => {
    try {
      await agentApi.renameSession(id, title);
      set((s) => ({
        sessions: s.sessions.map((c) =>
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
    let sessId = get().activeId;
    if (!sessId) {
      try {
        const sess = await agentApi.createSession();
        sessId = sess.id;
        set((s) => ({
          sessions: [toMeta(sess), ...s.sessions],
          activeId: sessId,
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
      await agentApi.sendMessage(sessId, content);
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

  retry: async () => {
    const { activeId, messages, isStreaming } = get();
    if (!activeId || isStreaming) return;

    const last = messages[messages.length - 1];
    if (!last || last.role !== "assistant") return;

    // 前端回撤：清空该消息已渲染的内容/工具调用/错误标记，回到"生成中"状态。
    // 后端 RetryMessage 会同步重置持久化数据，然后重新流式推送，
    // 由既有事件流（chunk/reasoning/tool/done）重新填充该消息。
    set((s) => ({
      messages: s.messages.map((m) =>
        m.id === last.id
          ? {
              ...m,
              content: "",
              reasoning: "",
              toolCalls: [],
              error: undefined,
              errorKind: undefined,
              usage: undefined,
              elapsedMs: undefined,
            }
          : m,
      ),
      isStreaming: true,
    }));

    try {
      await agentApi.retryMessage(activeId);
    } catch (e) {
      set((s) => ({
        isStreaming: false,
        messages: markLastAssistantError(s.messages, errText(e), "error"),
      }));
    }
  },

  deleteMessage: async (messageId) => {
    const { activeId, messages, isStreaming } = get();
    // 流式进行中禁止删除：后端同样拒绝（轮运行期间消息列表冻结，
    // 保证 agentHooks 的 assistantIndex 始终有效）
    if (!activeId || isStreaming) return;

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
  setBackendError: (msg) => set({ backendError: msg }),

  pickAndSetWorkspace: async () => {
    const { activeId } = get();
    try {
      const dir = await agentApi.openDirectoryDialog();
      if (!dir) return; // 用户取消
      if (!activeId) {
        // 无活动会话时先创建
        const sess = await agentApi.createSession();
        set((s) => ({
          sessions: [{ id: sess.id, title: sess.title, updatedAt: sess.updatedAt }, ...s.sessions],
          activeId: sess.id,
        }));
        await agentApi.setWorkspaceDir(sess.id, dir);
        const ws = await agentApi.getWorkspaceInfo(sess.id);
        set({ workspace: ws });
      } else {
        await agentApi.setWorkspaceDir(activeId, dir);
        // workspace:changed 事件会更新状态，但主动获取确保即时
        const ws = await agentApi.getWorkspaceInfo(activeId);
        set({ workspace: ws });
      }
    } catch (e) {
      set({ backendError: errText(e) });
    }
  },

  resetWorkspace: async () => {
    const { activeId } = get();
    if (!activeId) return;
    try {
      await agentApi.setWorkspaceDir(activeId, "");
      const ws = await agentApi.getWorkspaceInfo(activeId);
      set({ workspace: ws });
    } catch (e) {
      set({ backendError: errText(e) });
    }
  },

  refreshStats: async () => {
    const { activeId } = get();
    if (!activeId) {
      set({ stats: null });
      return;
    }
    try {
      const stats = await agentApi.getSessionStats(activeId);
      // 防止竞态：拉取期间用户切了会话
      if (get().activeId === activeId) {
        set({ stats });
      }
    } catch {
      // 统计失败静默忽略，不影响聊天主流程
    }
  },

  refreshModelInfo: async () => {
    try {
      set({ model: await agentApi.getModelInfo() });
    } catch {
      // 静默失败：TopicBar 退化为不显示模型名
    }
  },

  refreshModels: async () => {
    try {
      set({ models: await agentApi.listModels() });
    } catch {
      // 静默失败：切换下拉退化为空列表
    }
  },

  setActiveModel: async (id) => {
    try {
      await agentApi.setActiveModel(id);
      // 成功后端广播 model:changed，onModelChanged 统一刷新状态
    } catch (e) {
      set({ backendError: errText(e) });
    }
  },
}));
