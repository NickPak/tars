import { Circle } from "lucide-react";
import { useTranslation } from "react-i18next";
import { useChatStore } from "../store/chatStore";

/**
 * 底部状态栏 —— 会话级聚合状态（Reasonix 风格）：
 *   运行灯 | 平均命中 | 会话 tokens | Credits | 轮次 | 上下文 | 压缩阈值 | 会话费用
 * 轮次级指标（本次命中率/本次费用/本轮 tokens/耗时）在每条消息的底部展示。
 */
export default function StatusBar() {
  const { t } = useTranslation();
  const stats = useChatStore((s) => s.stats);
  const isStreaming = useChatStore((s) => s.isStreaming);
  const backendError = useChatStore((s) => s.backendError);

  // 运行灯只表达即时状态：后端错误（红）/ 生成中（黄）/ 空闲（暗）
  const lampColor = backendError ? "var(--error)" : isStreaming ? "var(--attention)" : "var(--idle)";
  const lampTitle = backendError
    ? t("statusbar.backendError")
    : isStreaming ? t("statusbar.streaming") : t("statusbar.idle");

  return (
    <footer className="statusbar">
      <div className="statusbar-left">
        <span className="statusbar-item" title={lampTitle}>
          <Circle size={8} fill={lampColor} color={lampColor} />
        </span>
      </div>
      <div className="statusbar-right">
        {stats && (
          <>
            <span className="statusbar-item" title={t("statusbar.avgHitTip")}>
              {t("statusbar.avgHit", { value: formatPercent(stats.avgCacheHitRate) })}
            </span>
            <span className="statusbar-item" title={t("statusbar.tokensTip")}>
              {t("statusbar.tokens", { value: formatTokens(stats.totalTokens) })}
            </span>
            <span className="statusbar-item" title={t("statusbar.creditsTip")}>
              {t("statusbar.credits", { value: stats.totalCredits.toFixed(1) })}
            </span>
            <span className="statusbar-item" title={t("statusbar.roundsTip")}>
              {t("statusbar.rounds", { count: stats.rounds })}
            </span>
            <span
              className="statusbar-item"
              title={t("statusbar.contextTip", {
                used: formatTokens(Math.round(stats.contextUsage * stats.contextWindow)),
                total: formatTokens(stats.contextWindow),
              })}
            >
              {t("statusbar.context", { value: formatPercent(stats.contextUsage) })}
            </span>
            <span className="statusbar-item" title={t("statusbar.thresholdTip")}>
              {t("statusbar.threshold", { value: formatPercent(stats.compressionThreshold) })}
            </span>
            {(stats.compressionCount ?? 0) > 0 && (
              <span
                className="statusbar-item"
                title={t("statusbar.compressedTip", {
                  count: stats.compressionCount,
                  rate: formatPercent(stats.lastCompressionRecovery ?? 0),
                })}
              >
                {t("statusbar.compressed", { count: stats.compressionCount })}
              </span>
            )}
            {stats.inputPricePerMillion > 0 && (
              <span className="statusbar-item" title={t("statusbar.costTip")}>
                {t("statusbar.cost", { value: stats.totalCostYuan.toFixed(4) })}
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
