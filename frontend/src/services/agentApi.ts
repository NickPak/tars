// 接口绑定层：封装所有对 Go 端 AgentService 的调用与流式事件订阅。
// UI 组件只依赖本模块，不直接接触 Wails runtime。
//
// 底层使用 `wails3 generate bindings` 生成的强类型 bindings
// （frontend/bindings/tars）。Go 端结构体/方法签名变更后重新生成即可，
// 本模块对上层（store / 组件）暴露的接口保持不变。

import { Events } from "@wailsio/runtime";
import { AgentService } from "../../bindings/tars";
import type { AppConfigView, ChatMessage, Conversation, FileEntry, ModelInfo, SessionStats, WorkspaceInfo } from "../types";
import { AgentEvents } from "../types";
import type { StreamChunk, StreamDone, StreamError } from "../types";
import type { ConversationRenamedEvent, ModelChangedEvent, ReasoningEvent, ToolEvent, ToolResultEvent, WorkspaceChangedEvent } from "../types";

export const agentApi = {
  createConversation: async (): Promise<Conversation> => {
    const conv = await AgentService.CreateConversation();
    if (!conv) throw new Error("创建会话失败：后端返回空");
    return conv as Conversation;
  },

  listConversations: async (): Promise<Conversation[]> =>
    (await AgentService.ListConversations()) as Conversation[],

  getConversation: async (id: string): Promise<Conversation> => {
    const conv = await AgentService.GetConversation(id);
    if (!conv) throw new Error(`会话不存在：${id}`);
    return conv as Conversation;
  },

  deleteConversation: (id: string): Promise<void> =>
    AgentService.DeleteConversation(id),

  renameConversation: (id: string, title: string): Promise<void> =>
    AgentService.RenameConversation(id, title),

  sendMessage: async (conversationId: string, text: string): Promise<ChatMessage | null> =>
    (await AgentService.SendMessage(conversationId, text)) as ChatMessage | null,

  cancelMessage: (conversationId: string): Promise<void> =>
    AgentService.CancelMessage(conversationId),

  /** 重试生成最后一条 assistant 回复（超时/出错后由用户触发） */
  retryMessage: async (conversationId: string): Promise<ChatMessage | null> =>
    (await AgentService.RetryMessage(conversationId)) as ChatMessage | null,

  deleteMessage: (conversationId: string, messageId: string): Promise<void> =>
    AgentService.DeleteMessage(conversationId, messageId),

  // --- 文件服务 ---

  listWorkspaceFiles: async (conversationId: string): Promise<FileEntry[]> =>
    (await AgentService.ListWorkspaceFiles(conversationId)) as FileEntry[],

  /** 用系统默认程序打开文件 */
  openFile: (conversationId: string, relPath: string): Promise<void> =>
    AgentService.OpenFile(conversationId, relPath),

  /** 在系统文件管理器中打开工作区目录 */
  revealInExplorer: (conversationId: string): Promise<void> =>
    AgentService.RevealInExplorer(conversationId),

  /** 在系统文件管理器中显示指定文件（选中该文件） */
  revealFileInExplorer: (conversationId: string, relPath: string): Promise<void> =>
    AgentService.RevealFileInExplorer(conversationId, relPath),

  // --- 工作区管理 ---

  /** 弹出系统目录选择对话框，返回选中的路径（取消则返回空串） */
  openDirectoryDialog: async (): Promise<string> =>
    await AgentService.OpenDirectoryDialog(),

  /** 设置会话的自定义工作区目录（空串 = 重置为默认） */
  setWorkspaceDir: (conversationId: string, dir: string): Promise<void> =>
    AgentService.SetWorkspaceDir(conversationId, dir),

  /** 获取会话当前的工作区信息 */
  getWorkspaceInfo: async (conversationId: string): Promise<WorkspaceInfo> =>
    (await AgentService.GetWorkspaceInfo(conversationId)) as WorkspaceInfo,

  /** 获取会话级聚合统计（状态栏数据） */
  getSessionStats: async (conversationId: string): Promise<SessionStats> =>
    (await AgentService.GetSessionStats(conversationId)) as SessionStats,

  /** 获取当前激活模型信息（TopicBar 模型选择器展示用） */
  getModelInfo: async (): Promise<ModelInfo> =>
    (await AgentService.GetModelInfo()) as ModelInfo,

  /** 获取全部已配置模型条目（模型切换下拉用） */
  listModels: async (): Promise<ModelInfo[]> =>
    ((await AgentService.ListModels()) ?? []) as ModelInfo[],

  /** 切换当前使用的模型（立即生效并持久化），成功后后端广播 model:changed */
  setActiveModel: (id: string): Promise<void> =>
    AgentService.SetActiveModel(id),

  /** 导出会话为 Markdown（弹出系统保存对话框），返回保存路径（取消返回空串） */
  exportConversation: async (conversationId: string): Promise<string> =>
    await AgentService.ExportConversation(conversationId),

  // --- 设置（应用配置） ---

  /** 获取当前应用配置（apiKey 脱敏为空串，用 apiKeySet 判断是否已配置） */
  getAppConfig: async (): Promise<AppConfigView> => {
    const cfg = await AgentService.GetAppConfig();
    if (!cfg) throw new Error("获取配置失败：后端返回空");
    return cfg as AppConfigView;
  },

  /** 保存应用配置：写回 config.yaml 并热更新（model/agent/trace 立即生效） */
  saveAppConfig: (cfg: AppConfigView): Promise<void> =>
    AgentService.SaveAppConfig(cfg),
};

export interface AgentEventHandlers {
  onChunk: (chunk: StreamChunk) => void;
  onDone: (done: StreamDone) => void;
  onError: (err: StreamError) => void;
  onConversationRenamed?: (ev: ConversationRenamedEvent) => void;
  onReasoning?: (ev: ReasoningEvent) => void;
  onTool?: (ev: ToolEvent) => void;
  onToolResult?: (ev: ToolResultEvent) => void;
  onWorkspaceChanged?: (ev: WorkspaceChangedEvent) => void;
  onModelChanged?: (ev: ModelChangedEvent) => void;
}

/**
 * 订阅 Agent 流式事件，返回解绑函数。
 * 后端约定：SendMessage 后陆续发出 "agent:chunk"，
 * 最后发出恰好一个 "agent:done" 或 "agent:error"。
 * 会话标题变更时发出 "conversation:renamed"。
 * 模型思考过程通过 "agent:reasoning" 推送。
 */
export function subscribeAgentEvents(handlers: AgentEventHandlers): () => void {
  const unsubs = [
    Events.On(AgentEvents.Chunk, (ev) => handlers.onChunk(ev.data as StreamChunk)),
    Events.On(AgentEvents.Done, (ev) => handlers.onDone(ev.data as StreamDone)),
    Events.On(AgentEvents.Error, (ev) => handlers.onError(ev.data as StreamError)),
    Events.On(AgentEvents.ConversationRenamed, (ev) =>
      handlers.onConversationRenamed?.(ev.data as ConversationRenamedEvent),
    ),
    Events.On(AgentEvents.Reasoning, (ev) =>
      handlers.onReasoning?.(ev.data as ReasoningEvent),
    ),
    Events.On(AgentEvents.Tool, (ev) =>
      handlers.onTool?.(ev.data as ToolEvent),
    ),
    Events.On(AgentEvents.ToolResult, (ev) =>
      handlers.onToolResult?.(ev.data as ToolResultEvent),
    ),
    Events.On(AgentEvents.WorkspaceChanged, (ev) =>
      handlers.onWorkspaceChanged?.(ev.data as WorkspaceChangedEvent),
    ),
    Events.On(AgentEvents.ModelChanged, (ev) =>
      handlers.onModelChanged?.(ev.data as ModelChangedEvent),
    ),
  ];
  return () => unsubs.forEach((unsub) => unsub());
}

/**
 * 订阅"文件可能已变更"的事件：工具执行完成（agent:tool_result，写文件工具
 * 执行完毕的时刻）和一轮回复结束（agent:done，兜底）。仅当事件属于
 * conversationId 对应的会话时才触发 callback。返回解绑函数。
 */
export function subscribeFileChanges(
  conversationId: string,
  callback: () => void,
): () => void {
  const unsubs = [
    Events.On(AgentEvents.ToolResult, (ev) => {
      if ((ev.data as ToolResultEvent).conversationId === conversationId) {
        callback();
      }
    }),
    Events.On(AgentEvents.Done, (ev) => {
      if ((ev.data as StreamDone).conversationId === conversationId) {
        callback();
      }
    }),
  ];
  return () => unsubs.forEach((unsub) => unsub());
}
