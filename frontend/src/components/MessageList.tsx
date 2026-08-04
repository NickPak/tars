import { useEffect, useRef, useState } from "react";
import { useChatStore } from "../store/chatStore";
import type { UsageInfo } from "../types";
import Markdown from "./Markdown";

export default function MessageList() {
  const messages = useChatStore((s) => s.messages);
  const isStreaming = useChatStore((s) => s.isStreaming);
  const deleteMessage = useChatStore((s) => s.deleteMessage);
  const bottomRef = useRef<HTMLDivElement>(null);

  // 消息更新（含流式追加）时滚动到底部
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
      </div>
    );
  }

  return (
    <div className="message-list">
      <div className="message-list-inner">
        {messages.map((m, i) => {
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
                    <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                      <polyline points="3 6 5 6 21 6" />
                      <path d="M19 6v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6m3 0V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2" />
                    </svg>
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
                    <div className="message-error">生成失败:{m.error}</div>
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
        <svg
          className={`reasoning-chevron${expanded ? " expanded" : ""}`}
          viewBox="0 0 24 24"
          fill="none"
          stroke="currentColor"
          strokeWidth="2"
          strokeLinecap="round"
          strokeLinejoin="round"
        >
          <polyline points="9 18 15 12 9 6" />
        </svg>
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
            <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
              <polyline points="20 6 9 17 4 12" />
            </svg>
          ) : (
            <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
              <circle cx="12" cy="12" r="10" strokeDasharray="42" strokeDashoffset="0" />
            </svg>
          )}
        </span>
        <span className="tool-card-name">{name}</span>
        {args && <span className="tool-card-args">{args.length > 80 ? args.slice(0, 80) + "…" : args}</span>}
        <svg className={`tool-card-chevron${expanded ? " expanded" : ""}`} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
          <polyline points="9 18 15 12 9 6" />
        </svg>
      </button>
      {expanded && (
        <div className="tool-card-body">
          {output !== undefined && (
            <pre className="tool-card-output">{output.length > 2000 ? output.slice(0, 2000) + "\n[truncated]" : output}</pre>
          )}
        </div>
      )}
    </div>
  );
}

/** 消息状态栏：copy / 点赞 / 点踩 / 删除 / credits / tokens / elapsed */
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

  const handleCopy = () => {
    navigator.clipboard.writeText(content).then(() => {
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    });
  };

  // Credits 估算：用总 token 数近似（1 credit ≈ 10K tokens）
  const credits = usage ? (usage.totalTokens / 10_000).toFixed(1) : undefined;

  return (
    <div className="msg-status-bar">
      <button className="msg-action" title="复制" onClick={handleCopy}>
        {copied ? (
          <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
            <polyline points="20 6 9 17 4 12" />
          </svg>
        ) : (
          <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
            <rect x="9" y="9" width="13" height="13" rx="2" ry="2" />
            <path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1" />
          </svg>
        )}
      </button>
      <button className="msg-action" title="赞">
        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
          <path d="M14 9V5a3 3 0 0 0-3-3l-4 9v11h11.28a2 2 0 0 0 2-1.7l1.38-9a2 2 0 0 0-2-2.3z" />
          <path d="M7 22H4a2 2 0 0 1-2-2v-7a2 2 0 0 1 2-2h3" />
        </svg>
      </button>
      <button className="msg-action" title="踩">
        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
          <path d="M10 15v4a3 3 0 0 0 3 3l4-9V2H6.72a2 2 0 0 0-2 1.7l-1.38 9a2 2 0 0 0 2 2.3z" />
          <path d="M17 2h2.67A2.31 2.31 0 0 1 22 4v7a2.31 2.31 0 0 1-2.33 2H17" />
        </svg>
      </button>
      <button className="msg-action msg-action-danger" title="删除" onClick={onDelete}>
        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
          <polyline points="3 6 5 6 21 6" />
          <path d="M19 6v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6m3 0V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2" />
        </svg>
      </button>
      {credits && (
        <span className="msg-usage">
          <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
            <circle cx="12" cy="12" r="10" />
            <path d="M12 6v6l4 2" />
          </svg>
          Credits {credits}
        </span>
      )}
      {usage && (
        <span className="msg-usage">
          <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
            <rect x="2" y="7" width="20" height="14" rx="2" ry="2" />
            <path d="M16 21V5a2 2 0 0 0-2-2h-4a2 2 0 0 0-2 2v16" />
          </svg>
          Tokens {formatTokens(usage.totalTokens)}
        </span>
      )}
      {elapsedMs !== undefined && (
        <span className="msg-usage">
          <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
            <circle cx="12" cy="12" r="10" />
            <path d="M12 6v6l4 2" />
          </svg>
          Elapsed {formatElapsed(elapsedMs)}
        </span>
      )}
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
