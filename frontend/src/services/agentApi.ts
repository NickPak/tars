// 接口绑定层：封装所有对 Go 端 AgentService 的调用与流式事件订阅。
// UI 组件只依赖本模块，不直接接触 Wails runtime。
//
// 底层使用 `wails3 generate bindings` 生成的强类型 bindings
// （frontend/bindings/tars）。Go 端结构体/方法签名变更后重新生成即可，
// 本模块对上层（store / 组件）暴露的接口保持不变。

import { Events } from "@wailsio/runtime";
import { AgentService } from "../../bindings/tars";
import type * as configModels from "../../bindings/tars/internal/config/models";
import type * as llmModels from "../../bindings/tars/pkg/llm/models";
import type { AppConfig, ChatMessage, Session, FileEntry, ModelInfo, SessionStats, WorkspaceInfo } from "../types";
import { AgentEvents } from "../types";
import type { StreamChunk, StreamDone, StreamError } from "../types";
import type { SessionRenamedEvent, ModelChangedEvent, ReasoningEvent, ToolEvent, ToolResultEvent, WorkspaceChangedEvent } from "../types";

/**
 * 后端 llm.Config 中 providers/models 是 map（key 即条目 ID），
 * 而 UI 需要有序数组（列表渲染、索引 key 等）。读取时在此转换：
 * map → 数组（key 作为 id 字段），并补齐 omitempty 缺省字段，
 * 让 UI 代码免于空判。
 */
function normalizeAppConfig(raw: configModels.AppConfig | null): AppConfig {
  const cfg = raw ?? {};
  const llm = cfg.llm;
  return {
    workDir: cfg.workDir ?? "",
    llm: {
      active: llm?.active ?? "",
      providers: Object.entries(llm?.providers ?? {}).map(([id, p]) => {
        const v = p ?? ({} as llmModels.ProviderConfig);
        return {
          id, // map key 是身份的唯一来源
          type: v.type ?? "",
          apiKey: v.apiKey ?? "",
          baseUrl: v.baseUrl ?? "",
          accessKey: v.accessKey ?? "",
          secretKey: v.secretKey ?? "",
          region: v.region ?? "",
          cacheTTL: v.cacheTTL ?? "",
        };
      }),
      models: Object.entries(llm?.models ?? {}).map(([id, m]) => {
        const v = m ?? ({} as llmModels.ModelConfig);
        return {
          id,
          provider: v.provider ?? "",
          modelId: v.modelId ?? "",
          contextWindow: v.contextWindow ?? 0,
          inputPricePerMillion: v.inputPricePerMillion ?? 0,
          outputPricePerMillion: v.outputPricePerMillion ?? 0,
          maxTokens: v.maxTokens ?? 0,
          thinkingBudget: v.thinkingBudget ?? null,
          enableThinking: v.enableThinking ?? null,
        };
      }),
    },
    agent: {
      maxIterations: cfg.agent?.maxIterations ?? 100,
      compressionThreshold: cfg.agent?.compressionThreshold ?? 0.8,
      iterationTimeout: cfg.agent?.iterationTimeout ?? 0,
    },
    trace: {
      enabled: cfg.trace?.enabled ?? false,
      otlpHttpEndpoint: cfg.trace?.otlpHttpEndpoint ?? "",
      otlpGrpcEndpoint: cfg.trace?.otlpGrpcEndpoint ?? "",
    },
  };
}

/**
 * 保存方向的逆转换：UI 数组 → 后端 map（条目 id 作为 key）。
 * 数组中 id 重复会被 map 静默吞掉，这里提前报错。
 */
function toWireConfig(cfg: AppConfig): configModels.AppConfig {
  if (new Set(cfg.llm.providers.map((p) => p.id)).size !== cfg.llm.providers.length) {
    throw new Error("供应商 ID 重复");
  }
  if (new Set(cfg.llm.models.map((m) => m.id)).size !== cfg.llm.models.length) {
    throw new Error("模型条目 ID 重复");
  }
  return {
    workDir: cfg.workDir,
    llm: {
      active: cfg.llm.active,
      providers: Object.fromEntries(cfg.llm.providers.map((p) => [p.id, p])),
      models: Object.fromEntries(cfg.llm.models.map((m) => [m.id, m])),
    },
    agent: { ...cfg.agent },
    trace: { ...cfg.trace },
  };
}

export const agentApi = {
  createSession: async (): Promise<Session> => {
    const sess = await AgentService.CreateSession();
    if (!sess) throw new Error("创建会话失败：后端返回空");
    return sess as Session;
  },

  listSessions: async (): Promise<Session[]> =>
    (await AgentService.ListSessions()) as Session[],

  getSession: async (id: string): Promise<Session> => {
    const sess = await AgentService.GetSession(id);
    if (!sess) throw new Error(`会话不存在：${id}`);
    return sess as Session;
  },

  deleteSession: (id: string): Promise<void> =>
    AgentService.DeleteSession(id),

  renameSession: (id: string, title: string): Promise<void> =>
    AgentService.RenameSession(id, title),

  sendMessage: async (sessionId: string, text: string): Promise<ChatMessage | null> => {
    await AgentService.SendMessage(sessionId, text);
    return null; // assistant 回复通过流式事件推送，不由返回值携带
  },

  cancelMessage: (sessionId: string): Promise<void> =>
    AgentService.CancelMessage(sessionId),

  /** 重试生成最后一条 assistant 回复（超时/出错后由用户触发） */
  retryMessage: async (sessionId: string, messageId: string = ""): Promise<ChatMessage | null> => {
    await AgentService.RetryMessage(sessionId, messageId);
    return null;
  },

  deleteMessage: async (sessionId: string, messageId: string): Promise<void> => {
    await AgentService.DeleteMessage(sessionId, messageId);
  },

  // --- 文件服务 ---

  listWorkspaceFiles: async (sessionId: string): Promise<FileEntry[]> =>
    (await AgentService.ListWorkspaceFiles(sessionId)) as FileEntry[],

  /** 用系统默认程序打开文件 */
  openFile: (sessionId: string, relPath: string): Promise<void> =>
    AgentService.OpenFile(sessionId, relPath),

  /** 在系统文件管理器中打开工作区目录 */
  revealInExplorer: (sessionId: string): Promise<void> =>
    AgentService.RevealInExplorer(sessionId),

  /** 在系统文件管理器中显示指定文件（选中该文件） */
  revealFileInExplorer: (sessionId: string, relPath: string): Promise<void> =>
    AgentService.RevealFileInExplorer(sessionId, relPath),

  // --- 工作区管理 ---

  /** 弹出系统目录选择对话框，返回选中的路径（取消则返回空串） */
  openDirectoryDialog: async (): Promise<string> =>
    await AgentService.OpenDirectoryDialog(),

  /** 设置会话的自定义工作区目录（空串 = 重置为默认） */
  setWorkspaceDir: (sessionId: string, dir: string): Promise<void> =>
    AgentService.SetWorkspaceDir(sessionId, dir),

  /** 获取会话当前的工作区信息 */
  getWorkspaceInfo: async (sessionId: string): Promise<WorkspaceInfo> =>
    (await AgentService.GetWorkspaceInfo(sessionId)) as WorkspaceInfo,

  /** 获取会话级聚合统计（状态栏数据） */
  getSessionStats: async (sessionId: string): Promise<SessionStats> =>
    (await AgentService.GetSessionStats(sessionId)) as SessionStats,

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
  exportSession: async (sessionId: string): Promise<string> =>
    await AgentService.ExportSession(sessionId),

  // --- 设置（应用配置） ---

  /** 获取当前应用配置（密钥原样返回，UI 层负责掩码显示） */
  getAppConfig: async (): Promise<AppConfig> => {
    const cfg = await AgentService.GetAppConfig();
    if (!cfg) throw new Error("获取配置失败：后端返回空");
    return normalizeAppConfig(cfg);
  },

  /** 保存应用配置：写回 config.yaml 并热更新（model/agent/trace 立即生效） */
  saveAppConfig: async (cfg: AppConfig): Promise<void> => {
    await AgentService.SaveAppConfig(toWireConfig(cfg));
  },
};

export interface AgentEventHandlers {
  onChunk: (chunk: StreamChunk) => void;
  onDone: (done: StreamDone) => void;
  onError: (err: StreamError) => void;
  onSessionRenamed?: (ev: SessionRenamedEvent) => void;
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
 * 会话标题变更时发出 "session:renamed"。
 * 模型思考过程通过 "agent:reasoning" 推送。
 */
export function subscribeAgentEvents(handlers: AgentEventHandlers): () => void {
  const unsubs = [
    Events.On(AgentEvents.Chunk, (ev) => handlers.onChunk(ev.data as StreamChunk)),
    Events.On(AgentEvents.Done, (ev) => handlers.onDone(ev.data as StreamDone)),
    Events.On(AgentEvents.Error, (ev) => handlers.onError(ev.data as StreamError)),
    Events.On(AgentEvents.SessionRenamed, (ev) =>
      handlers.onSessionRenamed?.(ev.data as SessionRenamedEvent),
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
 * sessionId 对应的会话时才触发 callback。返回解绑函数。
 */
export function subscribeFileChanges(
  sessionId: string,
  callback: () => void,
): () => void {
  const unsubs = [
    Events.On(AgentEvents.ToolResult, (ev) => {
      if ((ev.data as ToolResultEvent).sessionId === sessionId) {
        callback();
      }
    }),
    Events.On(AgentEvents.Done, (ev) => {
      if ((ev.data as StreamDone).sessionId === sessionId) {
        callback();
      }
    }),
  ];
  return () => unsubs.forEach((unsub) => unsub());
}
