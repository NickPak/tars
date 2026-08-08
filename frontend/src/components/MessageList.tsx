import { useEffect, useRef, useState } from "react";
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
import type { UsageInfo } from "../types";
import Markdown from "./Markdown";

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
                <div className="user-bubble-group">
                  <div className="user-bubble">{m.content}</div>
                  <button
                    className="msg-action msg-action-danger user-delete-btn"
                    title="删除"
                    onClick={() => void deleteMessage(m.id)}
                  >
                    <Trash2 size={15} />
                  </button>
                </div>
              ) : (
                <div className="assistant-body">
                  {m.reasoning && (
                    <ReasoningBlock
                      content={m.reasoning}
                      streaming={streamingThis && !m.content}
                    />
                  )}
                  {m.toolCalls && m.toolCalls.length > 0 && (
                    <div className="tool-calls">
                      {m.toolCalls.map((tc) => (
                        <ToolCallCard
                          key={tc.id}
                          name={tc.name}
                          args={tc.args}
                          output={tc.output}
                        />
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
                      onDelete={() => void deleteMessage(m.id)}
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

/** 可折叠的思考过程区域 */
function ReasoningBlock({
  content,
  streaming,
}: {
  content: string;
  streaming: boolean;
}) {
  const [expanded, setExpanded] = useState(false);
  return (
    <div className="reasoning-block">
      <button
        className="reasoning-toggle"
        onClick={() => setExpanded((e) => !e)}
      >
        <ChevronRight
          size={14}
          className={`reasoning-chevron${expanded ? " expanded" : ""}`}
        />
        {streaming ? "思考中…" : "思考过程"}
      </button>
      {expanded && (
        <div className="reasoning-content">
          <Markdown content={content} />
        </div>
      )}
    </div>
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
  const status = output !== undefined ? "done" : "running";
  return (
    <div className={`tool-card tool-card-${status}`}>
      <button
        className="tool-card-header"
        onClick={() => setExpanded((e) => !e)}
      >
        <span className="tool-card-status">
          {status === "done" ? (
            <Check size={14} />
          ) : (
            <Circle size={14} className="tool-card-spinner" />
          )}
        </span>
        <span className="tool-card-name">{name}</span>
        {args && (
          <span className="tool-card-args">
            {args.length > 80 ? args.slice(0, 80) + "…" : args}
          </span>
        )}
        <ChevronRight
          size={14}
          className={`tool-card-chevron${expanded ? " expanded" : ""}`}
        />
      </button>
      {expanded && (
        <div className="tool-card-body">
          {output !== undefined && (
            <pre className="tool-card-output">
              {output.length > 2000
                ? output.slice(0, 2000) + "\n[truncated]"
                : output}
            </pre>
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

  // 本次费用 = prompt × 输入价 + completion × 输出价（价格未配置则不显示）
  const cost =
    usage && stats && stats.inputPricePerMillion > 0
      ? (usage.promptTokens * stats.inputPricePerMillion +
          usage.completionTokens * stats.outputPricePerMillion) /
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
      <button className="msg-action msg-action-danger" title="删除" onClick={onDelete}>
        <Trash2 size={16} />
      </button>
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
