import { create } from "zustand";
import { agentApi, subscribeAgentEvents } from "../services/agentApi";
import type { ChatMessage, Session, ModelInfo, SessionStats, WorkspaceInfo, ApprovalEvent, CompressionMark, Project } from "../types";

export interface SessionMeta {
  id: string;
  /** 所属项目 ID（侧边栏条目 = 项目，删除/工作区操作按项目进行） */
  projectId: string;
  title: string;
  createdAt: number;
  updatedAt: number;
}

interface ChatState {
  /** 全部项目（侧边栏列表） */
  projects: Project[];
  /** 全部会话的扁平索引（含 projectId；Tab 条按 activeProjectId 过滤） */
  sessions: SessionMeta[];
  /** 当前激活项目（Tab 条的作用域） */
  activeProjectId: string | null;
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
  /** 等待用户答复的危险调用审批（key: toolCallId），仅当前会话 */
  pendingApprovals: Record<string, ApprovalEvent>;
  /** 本轮锚点占位的本地 ID（submit/retry 返回前，流式事件按它归属首轮气泡） */
  streamAnchorId: string | null;
  /** 时间线上的压缩标记（仅本地运行时状态，随会话切换清空） */
  compressionMarks: CompressionMark[];

  /** 加载会话列表并订阅流式事件，返回清理函数 */
  init: () => () => void;
  /** 切换到空白新项目（首次发送时才真正创建） */
  newSession: () => void;
  selectSession: (id: string) => Promise<void>;
  /** 选中项目（侧边栏点击）：激活其最近会话 */
  selectProject: (projectId: string) => Promise<void>;
  /** 删除项目（侧边栏，级联其下全部会话） */
  deleteProject: (projectId: string) => Promise<void>;
  /** 显式重命名项目（侧边栏） */
  renameProject: (projectId: string, title: string) => Promise<void>;
  /** 在当前项目中新建会话 Tab */
  addSessionTab: () => Promise<void>;
  /** 关闭会话 Tab（删除该会话；项目保留） */
  closeSessionTab: (id: string) => Promise<void>;
  /** 重命名会话（Tab 右键菜单；项目显式标题优先于它展示） */
  renameSession: (id: string, title: string) => Promise<void>;
  /** 关闭当前项目中除指定会话外的全部 Tab */
  closeOtherTabs: (id: string) => Promise<void>;
  /** 关闭当前项目的全部 Tab（项目保留为空项目态） */
  closeAllTabs: () => Promise<void>;
  send: (text: string) => Promise<void>;
  cancel: () => Promise<void>;
  /** 重试生成最后一条 assistant 回复（先回撤已渲染内容，再重新流式） */
  retry: () => Promise<void>;
  deleteMessage: (messageId: string) => Promise<void>;
  /** 提交一次询问/审批的答复（value：confirm/deny、选项 id、文本、allow 等） */
  answerAsk: (toolCallId: string, value: string, reason?: string) => Promise<void>;
  dismissError: () => void;
  /** 设置后端错误横幅（供聊天域之外的组件复用错误提示通道） */
  setBackendError: (msg: string) => void;

  /** 弹出目录选择对话框并设置工作区（仅零消息窗口内生效，后端守卫） */
  pickAndSetWorkspace: () => Promise<void>;
  /** 拉取当前会话的聚合统计（done/error/切换会话后调用） */
  refreshStats: () => Promise<void>;
  /** 重新拉取模型信息（设置界面保存配置后调用，TopicBar/状态栏随之更新） */
  refreshModelInfo: () => Promise<void>;
  /** 重新拉取模型条目列表 */
  refreshModels: () => Promise<void>;
  /** 切换当前使用的模型（后端持久化并广播 model:changed） */
  setActiveModel: (id: string) => Promise<void>;
}

function toMeta(projectId: string, c: Session): SessionMeta {
  return { id: c.id, projectId, title: c.title, createdAt: c.createdAt, updatedAt: c.updatedAt };
}

function errText(e: unknown): string {
  return e instanceof Error ? e.message : String(e);
}

/** 历史加载归一化：tool 消息的输出按 toolCallId 合并进 assistant 的
 *  toolCalls（后端不持久化输出，输出在 role:"tool" 的独立消息里）。 */
function mergeToolOutputs(messages: ChatMessage[]): ChatMessage[] {
  const outputs = new Map<string, string>();
  for (const m of messages) {
    if (m.role === "tool" && m.toolCallId) outputs.set(m.toolCallId, m.content);
  }
  if (outputs.size === 0) return messages;
  return messages.map((m) => {
    if (m.role !== "assistant" || !m.toolCalls?.length) return m;
    return {
      ...m,
      toolCalls: m.toolCalls.map((tc) => ({
        ...tc,
        output: outputs.get(tc.id) ?? tc.output,
      })),
    };
  });
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

/** 解析流式事件的目标气泡（交错式存储，07 篇期 2）：
 *  按 messageId 匹配 assistant 气泡；未匹配时——若尾部气泡是本轮待回填的
 *  锚点占位（submit/retry 响应到达前的竞态窗口）则指向它，否则惰性创建
 *  新气泡（迭代 2+ 的新消息，后端每次迭代分配新 ID）。
 *  返回新数组与目标下标。 */
function resolveBubble(
  messages: ChatMessage[],
  messageId: string,
  anchorLocalId: string | null,
): [ChatMessage[], number] {
  const idx = messages.findIndex((m) => m.id === messageId && m.role === "assistant");
  if (idx >= 0) return [[...messages], idx];
  const last = messages[messages.length - 1];
  if (anchorLocalId && last?.role === "assistant" && last.id === anchorLocalId) {
    return [[...messages], messages.length - 1];
  }
  const next = [
    ...messages,
    { id: messageId, role: "assistant", content: "", createdAt: Date.now() } as ChatMessage,
  ];
  return [next, next.length - 1];
}

export const useChatStore = create<ChatState>((set, get) => ({
  projects: [],
  sessions: [],
  activeProjectId: null,
  activeId: null,
  messages: [],
  isStreaming: false,
  backendError: null,
  workspace: null,
  stats: null,
  model: null,
  models: [],
  pendingApprovals: {},
  streamAnchorId: null,
  compressionMarks: [],

  init: () => {
    agentApi
      .listProjects()
      .then((list) => {
        // 侧边栏条目 = 项目；标题取项目内最近会话的自动命名（展示层推导）
        const projects = list ?? [];
        const sessions: SessionMeta[] = [];
        for (const p of projects) {
          for (const sess of p.sessions ?? []) {
            sessions.push(toMeta(p.id, sess));
          }
        }
        set({ projects, sessions });
      })
      .catch((e) => set({ backendError: errText(e) }));

    // 加载当前模型配置与模型条目列表（应用级，不随会话变化）
    void get().refreshModelInfo();
    void get().refreshModels();

    return subscribeAgentEvents({
      onChunk: ({ sessionId, messageId, chunk }) => {
        if (!get().isStreaming || sessionId !== get().activeId) return;
        set((s) => {
          const [messages, idx] = resolveBubble(s.messages, messageId, s.streamAnchorId);
          messages[idx] = { ...messages[idx], content: messages[idx].content + chunk };
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
          return { messages, isStreaming: false, streamAnchorId: null };
        });
        void get().refreshStats();
      },
      onError: (err) => {
        if (err.sessionId !== get().activeId) return;
        set((s) => ({
          messages: markLastAssistantError(s.messages, err.error, err.kind),
          isStreaming: false,
          streamAnchorId: null,
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
      onReasoning: ({ sessionId, messageId, content }) => {
        if (!get().isStreaming || sessionId !== get().activeId) return;
        set((s) => {
          const [messages, idx] = resolveBubble(s.messages, messageId, s.streamAnchorId);
          messages[idx] = {
            ...messages[idx],
            reasoning: (messages[idx].reasoning ?? "") + content,
          };
          return { messages };
        });
      },
      onTool: ({ sessionId, messageId, toolCallId, toolName, args }) => {
        if (sessionId !== get().activeId) return;
        set((s) => {
          const [messages, idx] = resolveBubble(s.messages, messageId, s.streamAnchorId);
          const target = messages[idx];
          messages[idx] = {
            ...target,
            toolCalls: [...(target.toolCalls ?? []), { id: toolCallId, name: toolName, args }],
          };
          return { messages };
        });
      },
      onToolResult: ({ sessionId, toolCallId, output }) => {
        if (sessionId !== get().activeId) return;
        set((s) => {
          const messages = [...s.messages];
          // 自尾向前找发起该调用的气泡（交错式：结果配对到发起迭代的消息）
          let idx = -1;
          for (let i = messages.length - 1; i >= 0; i--) {
            const m = messages[i];
            if (m.role === "assistant" && m.toolCalls?.some((t) => t.id === toolCallId)) {
              idx = i;
              break;
            }
          }
          if (idx >= 0) {
            const target = messages[idx];
            messages[idx] = {
              ...target,
              toolCalls: target.toolCalls?.map((tc) =>
                tc.id === toolCallId ? { ...tc, output } : tc,
              ),
            };
          }
          // 结果到达 = 该调用的审批已了结
          const pendingApprovals = { ...s.pendingApprovals };
          delete pendingApprovals[toolCallId];
          return { messages, pendingApprovals };
        });
      },
      onApproval: (ev) => {
        if (ev.sessionId !== get().activeId) return;
        set((s) => ({
          pendingApprovals: { ...s.pendingApprovals, [ev.toolCallId]: ev },
        }));
      },
      onCompressionDone: (ev) => {
        if (ev.sessionId !== get().activeId) return;
        set((s) => ({
          compressionMarks: [
            ...s.compressionMarks,
            {
              id: crypto.randomUUID(),
              at: Date.now(),
              beforeTokens: ev.beforeTokens,
              afterTokens: ev.afterTokens,
              newEntries: ev.newEntries,
              durationMs: ev.durationMs,
            },
          ],
        }));
      },
      onCompressionFailed: (ev) => {
        if (ev.sessionId !== get().activeId) return;
        set((s) => ({
          compressionMarks: [
            ...s.compressionMarks,
            {
              id: crypto.randomUUID(),
              at: Date.now(),
              error: ev.error,
              circuitOpen: ev.circuitOpen,
            },
          ],
        }));
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
    set({ activeId: null, activeProjectId: null, messages: [], workspace: null, stats: null, pendingApprovals: {} });
  },

  selectSession: async (id) => {
    if (get().isStreaming || id === get().activeId) return;
    const meta = get().sessions.find((c) => c.id === id);
    set({ activeId: id, activeProjectId: meta?.projectId ?? get().activeProjectId, messages: [], workspace: null, stats: null, pendingApprovals: {}, compressionMarks: [] });
    try {
      const sess = await agentApi.getSession(id);
      set({ messages: mergeToolOutputs(sess.messages ?? []) });
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

  selectProject: async (projectId) => {
    if (get().isStreaming || projectId === get().activeProjectId) return;
    // 激活项目内最近创建的会话；无会话则停在空项目态（Tab 条只剩【+】）
    const tabs = get()
      .sessions.filter((c) => c.projectId === projectId)
      .sort((a, b) => a.createdAt - b.createdAt);
    const target = tabs[tabs.length - 1];
    if (target) {
      await get().selectSession(target.id);
    } else {
      set({ activeProjectId: projectId, activeId: null, messages: [], workspace: null, stats: null, pendingApprovals: {}, compressionMarks: [] });
    }
  },

  deleteProject: async (projectId) => {
    try {
      await agentApi.deleteProject(projectId);
      set((s) => ({
        projects: s.projects.filter((p) => p.id !== projectId),
        sessions: s.sessions.filter((c) => c.projectId !== projectId),
        ...(s.activeProjectId === projectId
          ? { activeId: null, activeProjectId: null, messages: [], workspace: null, stats: null }
          : {}),
      }));
    } catch (e) {
      set({ backendError: errText(e) });
    }
  },

  addSessionTab: async () => {
    const pid = get().activeProjectId;
    if (!pid || get().isStreaming) return;
    try {
      const sess = await agentApi.createSession(pid);
      set((s) => ({ sessions: [...s.sessions, toMeta(pid, sess)] }));
      await get().selectSession(sess.id);
    } catch (e) {
      set({ backendError: errText(e) });
    }
  },

  closeSessionTab: async (id) => {
    // 流式中的活动 Tab 不可关（后端同样会拒绝删除运行中的会话）
    if (get().isStreaming && get().activeId === id) return;
    const meta = get().sessions.find((c) => c.id === id);
    if (!meta) return;
    try {
      await agentApi.deleteSession(id);
    } catch (e) {
      set({ backendError: errText(e) });
      return;
    }
    set((s) => ({ sessions: s.sessions.filter((c) => c.id !== id) }));
    if (get().activeId === id) {
      // 切到同项目相邻 Tab；没有则停在空项目态
      set({ activeId: null, messages: [], workspace: null, stats: null, pendingApprovals: {}, compressionMarks: [] });
      const rest = get()
        .sessions.filter((c) => c.projectId === meta.projectId)
        .sort((a, b) => a.createdAt - b.createdAt);
      const next = rest[rest.length - 1];
      if (next) await get().selectSession(next.id);
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

  closeOtherTabs: async (id) => {
    const pid = get().activeProjectId;
    if (!pid || get().isStreaming) return;
    const others = get().sessions.filter((c) => c.projectId === pid && c.id !== id);
    for (const t of others) {
      try {
        await agentApi.deleteSession(t.id);
      } catch (e) {
        set({ backendError: errText(e) });
        return;
      }
    }
    set((s) => ({
      sessions: s.sessions.filter((c) => c.projectId !== pid || c.id === id),
    }));
    if (get().activeId !== id) await get().selectSession(id);
  },

  closeAllTabs: async () => {
    const pid = get().activeProjectId;
    if (!pid || get().isStreaming) return;
    const all = get().sessions.filter((c) => c.projectId === pid);
    for (const t of all) {
      try {
        await agentApi.deleteSession(t.id);
      } catch (e) {
        set({ backendError: errText(e) });
        return;
      }
    }
    // 空项目态：项目保留，Tab 条只剩【+】
    set((s) => ({
      sessions: s.sessions.filter((c) => c.projectId !== pid),
      activeId: null,
      messages: [],
      workspace: null,
      stats: null,
      pendingApprovals: {},
      compressionMarks: [],
    }));
  },

  answerAsk: async (toolCallId, value, reason = "") => {
    try {
      await agentApi.answerAskUser(toolCallId, value, reason);
    } catch (e) {
      set({ backendError: errText(e) });
    }
  },

  renameProject: async (projectId, title) => {
    try {
      await agentApi.renameProject(projectId, title);
      set((s) => ({
        projects: s.projects.map((p) =>
          p.id === projectId ? { ...p, title } : p,
        ),
      }));
    } catch (e) {
      set({ backendError: errText(e) });
    }
  },

  send: async (text) => {
    const content = text.trim();
    if (!content || get().isStreaming) return;

    // 无活动会话时首次发送才落盘：有激活项目则在其中补建会话（Tab 全关
    // 后的空项目态），否则创建新项目（含默认会话）
    let sessId = get().activeId;
    if (!sessId) {
      try {
        const pid = get().activeProjectId;
        if (pid) {
          const sess = await agentApi.createSession(pid);
          sessId = sess.id;
          set((s) => ({
            sessions: [...s.sessions, toMeta(pid, sess)],
            activeId: sessId,
          }));
        } else {
          const proj = await agentApi.createProject();
          sessId = proj.session.id;
          set((s) => ({
            projects: [
              ...s.projects,
              { id: proj.id, workspaceDir: proj.workspaceDir, createdAt: proj.createdAt, updatedAt: proj.updatedAt, sessions: [proj.session] },
            ],
            sessions: [toMeta(proj.id, proj.session), ...s.sessions],
            activeId: sessId,
            activeProjectId: proj.id,
          }));
        }
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
      streamAnchorId: assistantMsg.id,
    }));

    try {
      const res = await agentApi.submitMessage(sessId, content);
      // 后端分配的消息 ID 回填本地占位（回填前的流式事件经 streamAnchorId
      // 归属首轮气泡，无竞态）；之后 DeleteMessage 等按 ID 操作无需等待
      // 会话重载即可生效。
      set((s) => ({
        messages: s.messages.map((m) =>
          m.id === userMsg.id
            ? { ...m, id: res.userMessageId }
            : m.id === assistantMsg.id
              ? { ...m, id: res.assistantMessageId }
              : m,
        ),
        streamAnchorId: null,
      }));
    } catch (e) {
      set((s) => ({
        messages: markLastAssistantError(s.messages, errText(e)),
        isStreaming: false,
        streamAnchorId: null,
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

    // 前端回撤：交错式一轮可能有多条 assistant 气泡——截断到本轮的
    // user 消息（含），再挂新的占位气泡；后端 PrepareRetry 同步截断持久化数据，
    // 新一轮由既有事件流（chunk/reasoning/tool/done）重新填充。
    let userIdx = -1;
    for (let i = messages.length - 1; i >= 0; i--) {
      if (messages[i].role === "user") {
        userIdx = i;
        break;
      }
    }
    if (userIdx < 0) return;
    const placeholder: ChatMessage = {
      id: crypto.randomUUID(),
      role: "assistant",
      content: "",
      createdAt: Date.now(),
    };
    set((s) => ({
      messages: [...s.messages.slice(0, userIdx + 1), placeholder],
      isStreaming: true,
      streamAnchorId: placeholder.id,
    }));

    try {
      const newAssistantID = await agentApi.retryMessage(activeId);
      // 新一轮的锚点 ID 回填占位（回填前的流式事件经 streamAnchorId 归属）
      set((s) => ({
        messages: s.messages.map((m) =>
          m.id === placeholder.id ? { ...m, id: newAssistantID } : m,
        ),
        streamAnchorId: null,
      }));
    } catch (e) {
      set((s) => ({
        isStreaming: false,
        streamAnchorId: null,
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
        // 无活动会话时先创建项目（再为其设置自定义工作区）
        const proj = await agentApi.createProject();
        const sess = proj.session;
        set((s) => ({
          projects: [
            ...s.projects,
            { id: proj.id, workspaceDir: proj.workspaceDir, createdAt: proj.createdAt, updatedAt: proj.updatedAt, sessions: [sess] },
          ],
          sessions: [toMeta(proj.id, sess), ...s.sessions],
          activeId: sess.id,
          activeProjectId: proj.id,
        }));
        await agentApi.setWorkspaceDir(sess.id, dir);
        const ws = await agentApi.getWorkspaceInfo(sess.id);
        set({ workspace: ws });
      } else {
        await agentApi.setWorkspaceDir(activeId, dir);
        const ws = await agentApi.getWorkspaceInfo(activeId);
        set({ workspace: ws });
      }
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
