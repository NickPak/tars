// 与 Go 端 agentservice.go 中的结构体一一对应。

export type Role = "user" | "assistant" | "tool";

export interface ChatMessage {
  id: string;
  role: Role;
  content: string;
  createdAt: number;
  /** 模型思考过程（reasoning content） */
  reasoning?: string;
  /** 工具调用信息 */
  toolCalls?: ToolCallInfo[];
  /** 工具调用 ID（tool role 消息用） */
  toolCallId?: string;
  /** token 用量统计 */
  usage?: UsageInfo;
  /** 耗时（毫秒） */
  elapsedMs?: number;
  /** 仅前端本地使用：标记该条消息生成失败，后端无需提供 */
  error?: string;
  /** 仅前端本地使用：错误分类（timeout 时展示"模型超时"提示与重试按钮） */
  errorKind?: "timeout" | "error";
}

/** 一次工具调用的信息 */
export interface ToolCallInfo {
  id: string;
  name: string;
  args: string;
  output?: string;
}

export interface Conversation {
  id: string;
  title: string;
  messages: ChatMessage[];
  createdAt: number;
  updatedAt: number;
}

/** "agent:chunk" 事件 payload：助手回复的一个增量片段 */
export interface StreamChunk {
  conversationId: string;
  messageId: string;
  chunk: string;
}

/** "agent:done" 事件 payload：一条回复流式输出完毕 */
export interface StreamDone {
  conversationId: string;
  messageId: string;
  usage?: UsageInfo;
  elapsedMs?: number;
}

/** "agent:error" 事件 payload：流式输出失败 */
export interface StreamError {
  conversationId: string;
  messageId: string;
  error: string;
  /** 错误分类："timeout"（模型调用超时，可能是服务商拥堵）| "error"（其他） */
  kind?: "timeout" | "error";
}

/** "conversation:renamed" 事件 payload：会话标题变更 */
export interface ConversationRenamedEvent {
  conversationId: string;
  title: string;
}

/** token 用量统计 */
export interface UsageInfo {
  promptTokens: number;
  completionTokens: number;
  totalTokens: number;
  /** 提示词中命中服务端缓存的 token 数（缓存命中率 = cachedTokens / promptTokens） */
  cachedTokens?: number;
}

/** 当前模型配置（不含敏感信息） */
export interface ModelInfo {
  modelId: string;
  contextWindow: number;
}

/** 会话级聚合统计（底部状态栏数据） */
export interface SessionStats {
  modelId: string;
  /** 最近一次 LLM 调用是否成功（状态灯） */
  modelHealthy: boolean;
  /** 会话轮次（user 消息数） */
  rounds: number;
  totalTokens: number;
  /** 1 credit = 1000 tokens */
  totalCredits: number;
  /** 会话累计费用（元） */
  totalCostYuan: number;
  /** 平均缓存命中率 0-1 */
  avgCacheHitRate: number;
  /** 上下文使用占比 0-1 */
  contextUsage: number;
  contextWindow: number;
  /** 压缩阈值 0-1 */
  compressionThreshold: number;
  /** 价格表（元/百万 token），前端算每条消息的本次费用 */
  inputPricePerMillion: number;
  outputPricePerMillion: number;
}

/** "agent:reasoning" 事件 payload：模型思考过程 */
export interface ReasoningEvent {
  conversationId: string;
  messageId: string;
  content: string;
}

/** "agent:tool" 事件 payload：模型发起了工具调用 */
export interface ToolEvent {
  conversationId: string;
  messageId: string;
  toolCallId: string;
  toolName: string;
  args: string;
}

/** "agent:tool_result" 事件 payload：一个工具执行完毕 */
export interface ToolResultEvent {
  conversationId: string;
  messageId: string;
  toolCallId: string;
  output: string;
}

/** 工作区文件树中的一个条目（文件或目录） */
export interface FileEntry {
  name: string;
  /** 相对于工作区根目录的路径 */
  path: string;
  isDir: boolean;
  /** 文件大小（字节），目录为 0 */
  size: number;
  /** 子条目（仅目录有） */
  children?: FileEntry[];
}

/** 工作区信息 */
export interface WorkspaceInfo {
  /** 当前工作区目录绝对路径 */
  path: string;
  /** 是否为用户自定义的外部目录 */
  isCustom: boolean;
  /** 目录名（用于显示） */
  name: string;
}

/** "workspace:changed" 事件 payload */
export interface WorkspaceChangedEvent {
  conversationId: string;
  path: string;
  isCustom: boolean;
}

/** 与 Go 端 main.go 中注册的事件名保持一致 */
export const AgentEvents = {
  Chunk: "agent:chunk",
  Done: "agent:done",
  Error: "agent:error",
  ConversationRenamed: "conversation:renamed",
  Reasoning: "agent:reasoning",
  Tool: "agent:tool",
  ToolResult: "agent:tool_result",
  WorkspaceChanged: "workspace:changed",
} as const;
