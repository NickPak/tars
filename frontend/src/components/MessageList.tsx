import { Fragment, useEffect, useMemo, useRef, useState } from "react";
import {
  Copy,
  Check,
  ThumbsUp,
  ThumbsDown,
  Trash2,
  ChevronRight,
  Circle,
  Clock,
  Coins,
  FolderOpen,
  AlertTriangle,
  RotateCcw,
} from "lucide-react";
import { useChatStore } from "../store/chatStore";
import type { ToolCallInfo, UsageInfo } from "../types";
import Markdown from "./Markdown";
import { ApprovalCard, AskUserCard } from "./AskCards";

export default function MessageList() {
  const messages = useChatStore((s) => s.messages);
  const isStreaming = useChatStore((s) => s.isStreaming);
  const deleteMessage = useChatStore((s) => s.deleteMessage);
  const pickAndSetWorkspace = useChatStore((s) => s.pickAndSetWorkspace);
  const bottomRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    bottomRef.current?.scrollIntoView({
      behavior: "instant" as ScrollBehavior,
      block: "end",
    });
  }, [messages]);

  if (messages.length === 0) {
    return (
      <div className="welcome">
        <h1 className="welcome-title">你好</h1>
        <p className="welcome-sub">有什么可以帮你的？</p>
        <button
          className="welcome-open-dir"
          onClick={() => void pickAndSetWorkspace()}
        >
          <FolderOpen size={18} />
          打开目录作为工作区
        </button>
      </div>
    );
  }

  return (
    <div className="message-list">
      <div className="message-list-inner">
        {messages.map((m, i) => {
          // tool 消息（历史会话中的工具执行结果）不单独渲染——
          // 其内容已通过 assistant 消息的 toolCalls 数组展示在 ToolCallCard 中
          if (m.role === "tool") return null;
          const streamingThis =
            isStreaming && i === messages.length - 1 && m.role === "assistant";
          return (
            <div key={m.id} className={`message ${m.role}`}>
              {m.role === "user" ? (
                <UserBubble
                  content={m.content}
                  onDelete={
                    isStreaming ? undefined : () => void deleteMessage(m.id)
                  }
                />
              ) : (
                <div className="assistant-body">
                  {m.reasoning && (
                    <ReasoningBlock
                      content={m.reasoning}
                      streaming={streamingThis && !m.content}
                    />
                  )}
                  {m.parts && m.parts.length > 0 ? (
                    // 按 ReAct 迭代交错渲染：每段文本后跟该轮的工具卡片
                    <>
                      {m.parts.map((part, pi) => (
                        <Fragment key={pi}>
                          {part.content && <Markdown content={part.content} />}
                          {part.toolCalls && part.toolCalls.length > 0 && (
                            <div className="tool-calls">
                              {part.toolCalls.map((tc) => (
                                <ToolEntry key={tc.id} tc={tc} />
                              ))}
                            </div>
                          )}
                        </Fragment>
                      ))}
                      {streamingThis && <span className="stream-cursor" />}
                    </>
                  ) : (
                    // 旧数据（无 parts）回退：工具卡片集中显示在文本前
                    <>
                      {m.toolCalls && m.toolCalls.length > 0 && (
                        <div className="tool-calls">
                          {m.toolCalls.map((tc) => (
                            <ToolEntry key={tc.id} tc={tc} />
                          ))}
                        </div>
                      )}
                      {m.content ? (
                        <Markdown content={m.content} />
                      ) : streamingThis && !m.reasoning ? (
                        <span className="stream-cursor" />
                      ) : null}
                      {streamingThis && m.content && (
                        <span className="stream-cursor" />
                      )}
                    </>
                  )}
                  {m.error && (
                    <ErrorBanner
                      error={m.error}
                      kind={m.errorKind}
                      isLast={i === messages.length - 1}
                    />
                  )}
                  {!streamingThis && m.content && (
                    <MessageStatusBar
                      content={m.content}
                      usage={m.usage}
                      elapsedMs={m.elapsedMs}
                      onDelete={
                        isStreaming ? undefined : () => void deleteMessage(m.id)
                      }
                    />
                  )}
                </div>
              )}
            </div>
          );
        })}
        <div ref={bottomRef} />
      </div>
    </div>
  );
}

/** 用户消息气泡：hover 显示复制/删除按钮；流式进行中隐藏删除（onDelete 缺省） */
function UserBubble({
  content,
  onDelete,
}: {
  content: string;
  onDelete?: () => void;
}) {
  const [copied, setCopied] = useState(false);

  const handleCopy = () => {
    navigator.clipboard.writeText(content).then(() => {
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    });
  };

  return (
    <div className="user-bubble-group">
      <div className="user-bubble">{content}</div>
      <div className="user-bubble-actions">
        <button className="msg-action" title="复制" onClick={handleCopy}>
          {copied ? <Check size={15} /> : <Copy size={15} />}
        </button>
        {onDelete && (
          <button
            className="msg-action msg-action-danger"
            title="删除"
            onClick={onDelete}
          >
            <Trash2 size={15} />
          </button>
        )}
      </div>
    </div>
  );
}

/** 可折叠的思考过程区域：
 *  思考中（streaming=true）默认展开 —— 用户能实时看到推理输出，不会误以为卡死；
 *  思考完毕自动收起 —— 让位给最终结果。用户手动点击后进入手动模式，
 *  之后的自动切换不再覆盖用户选择。
 */
function ReasoningBlock({
  content,
  streaming,
}: {
  content: string;
  streaming: boolean;
}) {
  // manual: null = 跟随自动状态；true/false = 用户手动锁定
  const [manual, setManual] = useState<boolean | null>(null);
  const [copied, setCopied] = useState(false);
  const expanded = manual ?? streaming;

  const copyReasoning = () => {
    void navigator.clipboard.writeText(content).then(() => {
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    });
  };

  return (
    <div className="reasoning-block">
      <div className="reasoning-head">
        <button
          className={`reasoning-toggle${streaming ? " thinking" : ""}`}
          onClick={() => setManual(!expanded)}
        >
          <ChevronRight
            size={14}
            className={`reasoning-chevron${expanded ? " expanded" : ""}`}
          />
          {streaming ? "Deep Thinking…" : "Deep Thinking"}
        </button>
        <button
          className="msg-action reasoning-copy"
          title="复制思考过程"
          onClick={copyReasoning}
        >
          {copied ? <Check size={14} /> : <Copy size={14} />}
        </button>
      </div>
      {expanded && (
        <div className="reasoning-content">
          <Markdown content={content} />
        </div>
      )}
    </div>
  );
}

/** 单个工具调用的渲染入口：ask_user 渲染询问卡片；
 *  危险调用在等待审批时叠加审批卡片；其余渲染普通工具卡片 */
function ToolEntry({ tc }: { tc: ToolCallInfo }) {
  const pending = useChatStore((s) => s.pendingApprovals[tc.id]);
  if (tc.name === "ask_user") {
    return <AskUserCard toolCall={tc} />;
  }
  return (
    <>
      <ToolCallCard name={tc.name} args={tc.args} output={tc.output} />
      {pending && <ApprovalCard approval={pending} />}
    </>
  );
}

/** 工具调用卡片：显示工具名、参数、执行结果 */
function ToolCallCard({
  name,
  args,
  output,
}: {
  name: string;
  args: string;
  output?: string;
}) {
  const [expanded, setExpanded] = useState(false);
  const [copied, setCopied] = useState(false);
  const [copiedOutput, setCopiedOutput] = useState(false);
  const status = output !== undefined ? "done" : "running";

  // 参数美化：合法 JSON 缩进展示，长参数更易读；非法则原文展示
  const prettyArgs = useMemo(() => {
    if (!args) return "";
    try {
      return JSON.stringify(JSON.parse(args), null, 2);
    } catch {
      return args;
    }
  }, [args]);

  const copyArgs = (e: React.MouseEvent) => {
    e.stopPropagation(); // 不触发展开/收起
    void navigator.clipboard.writeText(prettyArgs).then(() => {
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    });
  };

  // 复制完整结果（不受展示截断影响）
  const copyOutput = () => {
    if (output === undefined) return;
    void navigator.clipboard.writeText(output).then(() => {
      setCopiedOutput(true);
      setTimeout(() => setCopiedOutput(false), 1500);
    });
  };

  return (
    <div className={`tool-card tool-card-${status}`}>
      {expanded ? (
        // 展开态：头部与完整参数分块显示（名称+美化 JSON+换行+复制），
        // 点击任意一处（参数区除外）收起
        <div
          className="tool-card-head-block"
          role="button"
          tabIndex={0}
          onClick={() => setExpanded(false)}
          onKeyDown={(e) => {
            if (e.key === "Enter" || e.key === " ") {
              e.preventDefault();
              setExpanded(false);
            }
          }}
        >
          <div className="tool-card-titlebar">
            <span className="tool-card-status">
              {status === "done" ? (
                <Check size={14} />
              ) : (
                <Circle size={14} className="tool-card-spinner" />
              )}
            </span>
            <span className="tool-card-name">{name}</span>
            {args && (
              <button
                className="msg-action tool-card-copy"
                title="复制参数"
                onClick={copyArgs}
              >
                {copied ? <Check size={14} /> : <Copy size={14} />}
              </button>
            )}
            <ChevronRight size={14} className="tool-card-chevron expanded" />
          </div>
          {args && (
            // 点击/划选参数不触发收起
            <div
              className="tool-card-args-full"
              onClick={(e) => e.stopPropagation()}
            >
              {prettyArgs}
            </div>
          )}
        </div>
      ) : (
        // 折叠态：单行省略，信息密度优先；点击展开
        <button
          className="tool-card-header"
          onClick={() => setExpanded(true)}
        >
          <span className="tool-card-status">
            {status === "done" ? (
              <Check size={14} />
            ) : (
              <Circle size={14} className="tool-card-spinner" />
            )}
          </span>
          <span className="tool-card-name">{name}</span>
          {args && <span className="tool-card-args">{args}</span>}
          <ChevronRight size={14} className="tool-card-chevron" />
        </button>
      )}
      {expanded && (
        <div className="tool-card-body">
          {output !== undefined ? (
            <>
              <div className="tool-card-body-head">
                <button
                  className="msg-action"
                  title="复制结果"
                  onClick={copyOutput}
                >
                  {copiedOutput ? <Check size={14} /> : <Copy size={14} />}
                </button>
              </div>
              <pre className="tool-card-output">
                {output.length > 2000
                  ? output.slice(0, 2000) + "\n[truncated]"
                  : output}
              </pre>
            </>
          ) : (
            <div className="tool-card-pending">执行中…</div>
          )}
        </div>
      )}
    </div>
  );
}

/** 错误提示横幅：超时给出针对性说明，最后一条出错消息提供"重试"入口 */
function ErrorBanner({
  error,
  kind,
  isLast,
}: {
  error: string;
  kind?: "timeout" | "error";
  isLast: boolean;
}) {
  const retry = useChatStore((s) => s.retry);
  const isStreaming = useChatStore((s) => s.isStreaming);

  return (
    <div className="message-error">
      <div className="message-error-main">
        <AlertTriangle size={15} className="message-error-icon" />
        <div className="message-error-text">
          {kind === "timeout" ? (
            <>
              <span className="message-error-title">
                模型响应超时 — 已等待 2 分钟仍无回复
              </span>
              <span className="message-error-detail">
                服务商当前可能繁忙或排队较长，你可以重试一次。
              </span>
            </>
          ) : (
            <>
              <span className="message-error-title">生成失败</span>
              <span className="message-error-detail">{error}</span>
            </>
          )}
        </div>
      </div>
      {isLast && (
        <button
          className="message-error-retry"
          disabled={isStreaming}
          onClick={() => void retry()}
        >
          <RotateCcw size={14} />
          重试
        </button>
      )}
    </div>
  );
}

/** 消息状态栏：操作按钮 + 轮次级指标（本次命中率 / 本次费用 / tokens / 耗时） */
function MessageStatusBar({
  content,
  usage,
  elapsedMs,
  onDelete,
}: {
  content: string;
  usage?: UsageInfo;
  elapsedMs?: number;
  onDelete?: () => void;
}) {
  const [copied, setCopied] = useState(false);
  // 价格表来自会话统计（后端配置），用于估算本次费用
  const stats = useChatStore((s) => s.stats);

  const handleCopy = () => {
    navigator.clipboard.writeText(content).then(() => {
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    });
  };

  // 本次缓存命中率 = cachedTokens / promptTokens（undefined 表示模型未返回缓存数据）
  const hitRate =
    usage && usage.promptTokens > 0 && usage.cachedTokens !== undefined
      ? usage.cachedTokens / usage.promptTokens
      : undefined;

  // 本次费用 = prompt × 输入价 + completion × 输出价。
  // 多模型下按产生该用量的模型条目价格核算（usage.entryId），
  // 条目已删除或无条目信息时回退当前激活模型价格；价格未配置则不显示。
  const price =
    (usage?.entryId && stats?.modelPrices?.[usage.entryId]) ||
    (stats
      ? { input: stats.inputPricePerMillion, output: stats.outputPricePerMillion }
      : undefined);
  const cost =
    usage && price && (price.input > 0 || price.output > 0)
      ? (usage.promptTokens * price.input +
          usage.completionTokens * price.output) /
        1e6
      : undefined;

  return (
    <div className="msg-status-bar">
      <button className="msg-action" title="复制" onClick={handleCopy}>
        {copied ? <Check size={16} /> : <Copy size={16} />}
      </button>
      <button className="msg-action" title="赞">
        <ThumbsUp size={16} />
      </button>
      <button className="msg-action" title="踩">
        <ThumbsDown size={16} />
      </button>
      {onDelete && (
        <button
          className="msg-action msg-action-danger"
          title="删除"
          onClick={onDelete}
        >
          <Trash2 size={16} />
        </button>
      )}
      <span className="msg-status-metrics">
        {hitRate !== undefined && (
          <span className="msg-usage" title="本次缓存命中率（cachedTokens / promptTokens）">
            本次命中 {(hitRate * 100).toFixed(0)}%
          </span>
        )}
        {usage && (
          <span className="msg-usage" title={`输入 ${usage.promptTokens} / 输出 ${usage.completionTokens}`}>
            <Coins size={12} />
            Tokens {formatTokens(usage.totalTokens)}
          </span>
        )}
        {cost !== undefined && (
          <span className="msg-usage" title="本次费用（按当前价格表估算）">
            ¥{cost.toFixed(4)}
          </span>
        )}
        {elapsedMs !== undefined && (
          <span className="msg-usage" title="本轮总耗时（含所有工具迭代）">
            <Clock size={12} />
            {formatElapsed(elapsedMs)}
          </span>
        )}
      </span>
    </div>
  );
}

function formatTokens(n: number): string {
  if (n >= 1_000_000) return `${(n / 1_000_000).toFixed(1)}M`;
  if (n >= 1_000) return `${(n / 1_000).toFixed(1)}K`;
  return `${n}`;
}

function formatElapsed(ms: number): string {
  if (ms < 1000) return `${ms}ms`;
  const secs = Math.floor(ms / 1000);
  if (secs < 60) return `${secs}s`;
  const mins = Math.floor(secs / 60);
  const remSecs = secs % 60;
  if (remSecs === 0) return `${mins}m`;
  return `${mins}m ${remSecs}s`;
}
