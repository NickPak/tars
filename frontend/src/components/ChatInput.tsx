import { useRef, useState } from "react";
import { useChatStore } from "../store/chatStore";

export default function ChatInput() {
  const [value, setValue] = useState("");
  const isStreaming = useChatStore((s) => s.isStreaming);
  const send = useChatStore((s) => s.send);
  const cancel = useChatStore((s) => s.cancel);
  const taRef = useRef<HTMLTextAreaElement>(null);

  const autoResize = () => {
    const ta = taRef.current;
    if (!ta) return;
    ta.style.height = "auto";
    ta.style.height = Math.min(ta.scrollHeight, 240) + "px";
  };

  const doSend = () => {
    const text = value.trim();
    if (!text || isStreaming) return;
    setValue("");
    requestAnimationFrame(autoResize);
    void send(text);
  };

  return (
    <div className="composer">
      <div className="composer-box">
        <textarea
          ref={taRef}
          className="composer-input"
          placeholder="向 TARS 提问…"
          rows={1}
          value={value}
          onChange={(e) => {
            setValue(e.target.value);
            autoResize();
          }}
          onKeyDown={(e) => {
            // isComposing：避免中文输入法选词时误发送
            if (e.key === "Enter" && !e.shiftKey && !e.nativeEvent.isComposing) {
              e.preventDefault();
              doSend();
            }
          }}
        />
        {isStreaming ? (
          <button
            className="composer-btn stop"
            onClick={() => void cancel()}
            aria-label="停止生成"
            title="停止生成"
          >
            <svg viewBox="0 0 24 24" fill="currentColor">
              <rect x="7" y="7" width="10" height="10" rx="2" />
            </svg>
          </button>
        ) : (
          <button
            className="composer-btn send"
            onClick={doSend}
            disabled={!value.trim()}
            aria-label="发送"
            title="发送"
          >
            <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.2" strokeLinecap="round" strokeLinejoin="round">
              <line x1="12" y1="19" x2="12" y2="5" />
              <polyline points="5 12 12 5 19 12" />
            </svg>
          </button>
        )}
      </div>
      <div className="composer-hint">Enter 发送 · Shift + Enter 换行</div>
    </div>
  );
}
