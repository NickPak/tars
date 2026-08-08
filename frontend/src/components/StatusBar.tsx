import { Circle, Cpu, Clock, Coins } from "lucide-react";
import { useChatStore } from "../store/chatStore";

/**
 * 底部状态栏：显示连接状态、模型名、最近一条消息的 token 和耗时。
 */
export default function StatusBar() {
  const messages = useChatStore((s) => s.messages);
  const isStreaming = useChatStore((s) => s.isStreaming);
  const backendError = useChatStore((s) => s.backendError);

  // 取最后一条 assistant 消息的统计信息
  const lastAssistant = [...messages].reverse().find((m) => m.role === "assistant");
  const usage = lastAssistant?.usage;
  const elapsedMs = lastAssistant?.elapsedMs;

  const statusLabel = backendError ? "错误" : isStreaming ? "生成中…" : "就绪";
  const statusColor = backendError ? "#f28b82" : isStreaming ? "#fdd663" : "#81c995";

  return (
    <footer className="statusbar">
      <div className="statusbar-left">
        <Circle size={8} fill={statusColor} color={statusColor} />
        <span className="statusbar-text">{statusLabel}</span>
      </div>
      <div className="statusbar-right">
        <span className="statusbar-item">
          <Cpu size={12} />
          DeepSeek-R1
        </span>
        {usage && (
          <span className="statusbar-item">
            <Coins size={12} />
            {formatTokens(usage.totalTokens)} tokens
          </span>
        )}
        {elapsedMs !== undefined && (
          <span className="statusbar-item">
            <Clock size={12} />
            {formatElapsed(elapsedMs)}
          </span>
        )}
        <span className="statusbar-version">TARS v0.1.0</span>
      </div>
    </footer>
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
