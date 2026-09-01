import { Cpu, Circle } from "lucide-react";
import { useChatStore } from "../store/chatStore";

/**
 * 底部状态栏 —— 会话级聚合状态（Reasonix 风格）：
 *   模型+健康灯 | 平均命中 | 会话 tokens | Credits | 轮次 | 上下文 | 压缩阈值 | 会话费用
 * 轮次级指标（本次命中率/本次费用/本轮 tokens/耗时）在每条消息的底部展示。
 */
export default function StatusBar() {
  const stats = useChatStore((s) => s.stats);
  const isStreaming = useChatStore((s) => s.isStreaming);
  const backendError = useChatStore((s) => s.backendError);

  // 新会话（stats 为 null）时统计信息尚不存在，左侧模型区整体隐藏
  const healthy = stats?.modelHealthy ?? true;
  const lampColor = backendError || !healthy ? "#f28b82" : isStreaming ? "#fdd663" : "#81c995";
  const lampTitle = backendError || !healthy
    ? "模型最近调用失败"
    : isStreaming ? "生成中…" : "模型可用";

  return (
    <footer className="statusbar">
      <div className="statusbar-left">
        {stats && (
          <span className="statusbar-item" title={lampTitle}>
            <Circle size={8} fill={lampColor} color={lampColor} />
            <Cpu size={12} />
            {stats.modelId}
          </span>
        )}
      </div>
      <div className="statusbar-right">
        {stats && (
          <>
            <span className="statusbar-item" title="会话平均缓存命中率（Σcached / Σprompt）">
              平均命中 {formatPercent(stats.avgCacheHitRate)}
            </span>
            <span className="statusbar-item" title="会话累计 token 消耗">
              会话 tokens {formatTokens(stats.totalTokens)}
            </span>
            <span className="statusbar-item" title="会话累计 Credits（1 credit = 1000 tokens）">
              Credits {stats.totalCredits.toFixed(1)}
            </span>
            <span className="statusbar-item" title="会话轮次（提问次数）">
              {stats.rounds} 轮
            </span>
            <span
              className="statusbar-item"
              title={`上下文使用 ${formatTokens(Math.round(stats.contextUsage * stats.contextWindow))} / ${formatTokens(stats.contextWindow)}`}
            >
              上下文 {formatPercent(stats.contextUsage)}
            </span>
            <span className="statusbar-item" title="上下文压缩阈值（超过后应触发历史压缩）">
              压缩阈值 {formatPercent(stats.compressionThreshold)}
            </span>
            {(stats.compressionCount ?? 0) > 0 && (
              <span
                className="statusbar-item"
                title={`本会话已压缩 ${stats.compressionCount} 次，上次回收率 ${formatPercent(stats.lastCompressionRecovery ?? 0)}`}
              >
                已压缩 {stats.compressionCount} 次
              </span>
            )}
            {stats.inputPricePerMillion > 0 && (
              <span className="statusbar-item" title="会话累计费用（按当前价格表估算）">
                会话费用 ¥{stats.totalCostYuan.toFixed(4)}
              </span>
            )}
          </>
        )}
      </div>
    </footer>
  );
}

function formatPercent(ratio: number): string {
  return `${(ratio * 100).toFixed(0)}%`;
}

function formatTokens(n: number): string {
  if (n >= 1_000_000) return `${(n / 1_000_000).toFixed(1)}M`;
  if (n >= 1_000) return `${(n / 1_000).toFixed(1)}K`;
  return `${n}`;
}
