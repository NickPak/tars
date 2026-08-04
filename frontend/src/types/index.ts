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

/** 与 Go 端 main.go 中注册的事件名保持一致 */
export const AgentEvents = {
  Chunk: "agent:chunk",
  Done: "agent:done",
  Error: "agent:error",
  ConversationRenamed: "conversation:renamed",
  Reasoning: "agent:reasoning",
  Tool: "agent:tool",
  ToolResult: "agent:tool_result",
} as const;
