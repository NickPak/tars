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
  /** 按 ReAct 迭代有序的产出（assistant 角色）：交错渲染文本与工具卡片 */
  parts?: MessagePart[];
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

/** assistant 消息在一个 ReAct 迭代内的产出（本轮文本 + 工具调用）。
 *  按 parts 顺序交错渲染文本与工具卡片；content/toolCalls 聚合字段
 *  仍是全迭代拼接（复制、统计、旧数据回退渲染用）。 */
export interface MessagePart {
  content?: string;
  toolCalls?: ToolCallInfo[];
}

export interface Session {
  id: string;
  title: string;
  messages: ChatMessage[];
  createdAt: number;
  updatedAt: number;
}

/** "agent:chunk" 事件 payload：助手回复的一个增量片段 */
export interface StreamChunk {
  sessionId: string;
  messageId: string;
  chunk: string;
}

/** "agent:done" 事件 payload：一条回复流式输出完毕 */
export interface StreamDone {
  sessionId: string;
  messageId: string;
  usage?: UsageInfo;
  elapsedMs?: number;
}

/** "agent:error" 事件 payload：流式输出失败 */
export interface StreamError {
  sessionId: string;
  messageId: string;
  error: string;
  /** 错误分类："timeout"（模型调用超时，可能是服务商拥堵）| "error"（其他） */
  kind?: "timeout" | "error";
}

/** "session:renamed" 事件 payload：会话标题变更 */
export interface SessionRenamedEvent {
  sessionId: string;
  title: string;
}

/** 危险调用审批请求（"agent:approval" 事件负载，框架发起、模型不参与） */
export interface ApprovalEvent {
  sessionId: string;
  toolCallId: string;
  toolName: string;
  /** 待批准的危险内容（如完整命令） */
  summary: string;
  /** 命中的风险规则说明 */
  reason: string;
  timeoutSeconds: number;
}

/** ask_user 工具的调用参数（从工具事件 args JSON 解析） */
export interface AskUserParams {
  type: "confirm" | "select" | "input";
  question: string;
  options?: { id: string; label: string; description?: string }[];
  recommended?: string;
  timeout_seconds?: number;
  default?: string;
}

/** ask_user 的工具结果 JSON（已答复后的卡片展示用） */
export interface AskAnswerPayload {
  type: string;
  answer: string;
  label?: string;
  reason?: string;
  source: "user" | "timeout_default" | "rule";
}

/** token 用量统计 */
export interface UsageInfo {
  promptTokens: number;
  completionTokens: number;
  totalTokens: number;
  /** 提示词中命中服务端缓存的 token 数（缓存命中率 = cachedTokens / promptTokens） */
  cachedTokens?: number;
  /** 产生该用量的模型条目 ID（多模型下按条目价格表核算费用） */
  entryId?: string;
}

/** 模型条目信息（模型选择器/状态栏展示用） */
export interface ModelInfo {
  /** 配置条目 ID（models[].entryId） */
  entryId: string;
  /** 供应商 ID */
  provider: string;
  /** 供应商类型（gemini/openai） */
  providerType: string;
  /** 发送给 API 的真实模型名 */
  modelId: string;
  contextWindow: number;
  /** 是否为当前使用中的模型 */
  active: boolean;
}

/** "model:changed" 事件 payload：当前模型已切换 */
export interface ModelChangedEvent {
  model: ModelInfo;
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
  /** 全部模型条目的价格表（key = 条目 ID），按 usage.entryId 核算单条费用 */
  modelPrices?: Record<string, { input: number; output: number }>;
}

/** "agent:reasoning" 事件 payload：模型思考过程 */
export interface ReasoningEvent {
  sessionId: string;
  messageId: string;
  content: string;
}

/** "agent:tool" 事件 payload：模型发起了工具调用 */
export interface ToolEvent {
  sessionId: string;
  messageId: string;
  toolCallId: string;
  toolName: string;
  args: string;
}

/** "agent:tool_result" 事件 payload：一个工具执行完毕 */
export interface ToolResultEvent {
  sessionId: string;
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
  sessionId: string;
  path: string;
  isCustom: boolean;
}

// ===== 设置界面（直接镜像 Go 端 config / llm 包的结构体；密钥原样传输，
// UI 层默认掩码显示 + 眼睛按钮切换明文。缺省字段由 agentApi.getAppConfig
// 归一化补齐，因此这里的字段均为必填，UI 代码无需空判） =====

/** 供应商配置（llm.ProviderConfig） */
export interface ProviderConfig {
  id: string;
  /** 供应商类型（对应 eino 原生组件）：gemini | openai | claude | deepseek | qwen | ark | ollama | qianfan */
  type: string;
  apiKey: string;
  baseUrl: string;
  /** 千帆 Access Key */
  accessKey: string;
  /** 千帆 Secret Key */
  secretKey: string;
  /** 火山引擎区域（ark），默认 cn-beijing */
  region: string;
  /** Claude 自动前缀缓存："5m" | "1h" | "" 关闭 */
  cacheTTL: string;
}

/** 模型条目配置（llm.ModelConfig） */
export interface ModelConfig {
  /** 条目唯一 ID，"provider/modelId" 形式 */
  entryId: string;
  /** 引用的供应商 ID */
  provider: string;
  /** 发送给 API 的真实模型名（ark 类型为推理接入点 endpoint ID） */
  modelId: string;
  contextWindow: number;
  inputPricePerMillion: number;
  outputPricePerMillion: number;
  /** 最大输出 tokens（claude 必填；deepseek 默认 4096 上限 8192；其余可选） */
  maxTokens: number;
  /** 思考预算（仅 gemini）：null 默认 / -1 动态 / 0 关闭 / >0 固定预算 */
  thinkingBudget: number | null;
  /** 思考模式开关：null 默认 / true 开 / false 关（仅 deepseek/qwen/ark/ollama） */
  enableThinking: boolean | null;
}

/** LLM 配置（llm.Config）：供应商列表 + 模型列表 + 当前激活条目 */
export interface LLMConfig {
  active: string;
  providers: ProviderConfig[];
  models: ModelConfig[];
}

/** Agent 运行时配置（config.AgentConfig） */
export interface AgentConfig {
  maxIterations: number;
  compressionThreshold: number;
  /** time.Duration 的 JSON 形式为纳秒数 */
  iterationTimeout: number;
}

/** 追踪配置（config.TraceConfig） */
export interface TraceConfig {
  /** 追踪总开关：关闭时即使配置了端点也不产生任何 span */
  enabled: boolean;
  otlpHttpEndpoint: string;
  otlpGrpcEndpoint: string;
}

/** 完整应用配置（config.AppConfig） */
export interface AppConfig {
  llm: LLMConfig;
  workDir: string;
  agent: AgentConfig;
  trace: TraceConfig;
  skills: SkillsConfig;
}

/** Skills 索引档位阈值（config.SkillsConfig） */
export interface SkillsConfig {
  tierFullMax: number;
  tierResidentMax: number;
}

/** 已安装技能（skills.Skill） */
export interface Skill {
  name: string;
  description: string;
  category: string;
  source: string;
  hasScripts: boolean;
  fileCount: number;
}

/** 与 Go 端 main.go 中注册的事件名保持一致 */
export const AgentEvents = {
  Chunk: "agent:chunk",
  Done: "agent:done",
  Error: "agent:error",
  SessionRenamed: "session:renamed",
  Reasoning: "agent:reasoning",
  Tool: "agent:tool",
  ToolResult: "agent:tool_result",
  Approval: "agent:approval",
  WorkspaceChanged: "workspace:changed",
  ModelChanged: "model:changed",
} as const;
