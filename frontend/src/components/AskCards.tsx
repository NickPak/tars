/**
 * 人机交互卡片（plan/agent-tool-design-plan.md 2.13）：
 * - AskUserCard：模型经 ask_user 发起的结构化询问（confirm/select/input）。
 *   数据完全来自工具调用本身（args=问题，output=答复），历史回放自然一致。
 * - ApprovalCard：执行层拦截危险调用后的安全审批（agent:approval 事件驱动，
 *   仅挂在等待中的调用上）。超时未答后端按保守默认处理。
 */
import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import i18n from "../i18n";
import { CircleHelp, ShieldAlert, Star } from "lucide-react";
import { useChatStore } from "../store/chatStore";
import type {
  ApprovalEvent,
  AskAnswerPayload,
  AskUserParams,
  ToolCallInfo,
} from "../types";

/** 本地倒计时（后端以 timeout_seconds 兜底，这里只做展示） */
function useCountdown(seconds: number | undefined, active: boolean) {
  const [left, setLeft] = useState(seconds ?? 0);
  useEffect(() => {
    if (!active || !seconds) return;
    setLeft(seconds);
    const timer = setInterval(
      () => setLeft((v) => (v > 0 ? v - 1 : 0)),
      1000,
    );
    return () => clearInterval(timer);
  }, [seconds, active]);
  return active && seconds ? left : null;
}

function Countdown({ seconds, active }: { seconds?: number; active: boolean }) {
  const left = useCountdown(seconds, active);
  if (left === null) return null;
  return <span className="ask-countdown">{left}s</span>;
}

/** ask_user 询问卡片：待答时渲染交互控件，已答时渲染答复摘要 */
export function AskUserCard({ toolCall }: { toolCall: ToolCallInfo }) {
  const { t } = useTranslation();
  const answerAsk = useChatStore((s) => s.answerAsk);
  const [text, setText] = useState("");

  const params = parseArgs(toolCall.args);
  if (!params) {
    // 参数解析失败（理论上不该发生）：退化为原文展示
    return <pre className="tool-card-output">{toolCall.args}</pre>;
  }

  // 已答复：展示结果
  if (toolCall.output !== undefined) {
    const ans = parseAnswer(toolCall.output);
    return (
      <div className="ask-card ask-card-done">
        <div className="ask-card-head">
          <CircleHelp size={14} />
          <span className="ask-card-question">{params.question}</span>
        </div>
        <div className="ask-card-answer">
          {ans
            ? formatAnswer(params, ans)
            : toolCall.output}
          {ans?.source === "timeout_default" && (
            <span className="ask-card-note">{t("ask.timeoutNote")}</span>
          )}
        </div>
      </div>
    );
  }

  const submit = (value: string, reason = "") => {
    void answerAsk(toolCall.id, value, reason);
  };

  return (
    <div className="ask-card">
      <div className="ask-card-head">
        <CircleHelp size={14} />
        <span className="ask-card-question">{params.question}</span>
        <Countdown seconds={params.timeout_seconds} active />
      </div>

      {params.type === "confirm" && (
        <div className="ask-card-actions">
          <button className="ask-btn ask-btn-primary" onClick={() => submit("confirm")}>
            {t("ask.confirm")}
          </button>
          <button className="ask-btn" onClick={() => submit("deny")}>
            {t("ask.reject")}
          </button>
        </div>
      )}

      {params.type === "select" && (
        <div className="ask-options">
          {params.options?.map((o) => (
            <button
              key={o.id}
              className="ask-option"
              onClick={() => submit(o.id)}
            >
              <span className="ask-option-label">
                {o.label}
                {params.recommended?.startsWith(o.id) && (
                  <span className="ask-option-rec" title={params.recommended}>
                    <Star size={11} /> {t("ask.recommended")}
                  </span>
                )}
              </span>
              {o.description && (
                <span className="ask-option-desc">{o.description}</span>
              )}
            </button>
          ))}
        </div>
      )}

      {params.type === "input" && (
        <div className="ask-card-actions">
          <input
            className="ask-input"
            value={text}
            placeholder={t("ask.replyPlaceholder")}
            onChange={(e) => setText(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === "Enter" && text.trim()) submit(text.trim());
            }}
          />
          <button
            className="ask-btn ask-btn-primary"
            disabled={!text.trim()}
            onClick={() => submit(text.trim())}
          >
            {t("ask.submit")}
          </button>
        </div>
      )}
    </div>
  );
}

/** 危险调用审批卡片：允许一次 / 本会话常允许 / 拒绝（可附理由） */
export function ApprovalCard({ approval }: { approval: ApprovalEvent }) {
  const { t } = useTranslation();
  const answerAsk = useChatStore((s) => s.answerAsk);
  const [reason, setReason] = useState("");

  const submit = (value: string) => {
    void answerAsk(approval.toolCallId, value, value === "deny" ? reason : "");
  };

  return (
    <div className="ask-card approval-card">
      <div className="ask-card-head">
        <ShieldAlert size={14} />
        <span className="ask-card-question">
          {t("ask.ruleHit", { tool: approval.toolName, reason: approval.reason })}
        </span>
        <Countdown seconds={approval.timeoutSeconds} active />
      </div>
      <pre className="approval-summary">{approval.summary}</pre>
      <input
        className="ask-input"
        value={reason}
        placeholder={t("ask.denyPlaceholder")}
        onChange={(e) => setReason(e.target.value)}
      />
      <div className="ask-card-actions">
        <button className="ask-btn ask-btn-primary" onClick={() => submit("allow")}>
          {t("ask.allowOnce")}
        </button>
        <button className="ask-btn" onClick={() => submit("allow_always")}>
          {t("ask.allowSession")}
        </button>
        <button className="ask-btn ask-btn-danger" onClick={() => submit("deny")}>
          {t("ask.deny")}
        </button>
      </div>
    </div>
  );
}

function parseArgs(raw: string): AskUserParams | null {
  try {
    const v = JSON.parse(raw) as AskUserParams;
    return v && typeof v.question === "string" ? v : null;
  } catch {
    return null;
  }
}

function parseAnswer(raw: string): AskAnswerPayload | null {
  try {
    return JSON.parse(raw) as AskAnswerPayload;
  } catch {
    return null;
  }
}

function formatAnswer(params: AskUserParams, ans: AskAnswerPayload): string {
  if (ans.type === "confirm")
    return ans.answer === "confirm"
      ? i18n.t("ask.resultConfirmed")
      : i18n.t("ask.resultRejected");
  if (ans.type === "select") return ans.label ?? ans.answer;
  return ans.answer;
}
