import { useEffect, useRef, useState } from "react";

interface RenameDialogProps {
  open: boolean;
  oldTitle: string;
  onCancel: () => void;
  onConfirm: (title: string) => void;
}

/** 重命名会话的自定义模态对话框，替代 window.prompt。 */
export default function RenameDialog({
  open,
  oldTitle,
  onCancel,
  onConfirm,
}: RenameDialogProps) {
  const [value, setValue] = useState(oldTitle);
  const inputRef = useRef<HTMLInputElement>(null);

  // 打开时用旧标题填充并自动聚焦选中
  useEffect(() => {
    if (open) {
      setValue(oldTitle);
      // 延迟聚焦确保 DOM 已渲染
      requestAnimationFrame(() => {
        inputRef.current?.focus();
        inputRef.current?.select();
      });
    }
  }, [open, oldTitle]);

  // Esc 关闭，Enter 确认
  useEffect(() => {
    if (!open) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") onCancel();
      if (e.key === "Enter") {
        e.preventDefault();
        submit();
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open, value]);

  if (!open) return null;

  const submit = () => {
    const trimmed = value.trim();
    if (trimmed) onConfirm(trimmed);
  };

  return (
    <div className="dialog-overlay" onClick={onCancel}>
      <div className="dialog-box" onClick={(e) => e.stopPropagation()}>
        <div className="dialog-title">重命名对话</div>
        <input
          ref={inputRef}
          className="dialog-input"
          value={value}
          onChange={(e) => setValue(e.target.value)}
          placeholder="输入新的对话标题"
        />
        <div className="dialog-actions">
          <button className="dialog-btn secondary" onClick={onCancel}>
            取消
          </button>
          <button
            className="dialog-btn primary"
            onClick={submit}
            disabled={!value.trim()}
          >
            确定
          </button>
        </div>
      </div>
    </div>
  );
}

interface ConfirmDialogProps {
  open: boolean;
  message: string;
  onCancel: () => void;
  onConfirm: () => void;
}

/** 通用确认模态对话框，替代 window.confirm。 */
export function ConfirmDialog({
  open,
  message,
  onCancel,
  onConfirm,
}: ConfirmDialogProps) {
  useEffect(() => {
    if (!open) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") onCancel();
      if (e.key === "Enter") onConfirm();
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [open, onCancel, onConfirm]);

  if (!open) return null;

  return (
    <div className="dialog-overlay" onClick={onCancel}>
      <div className="dialog-box" onClick={(e) => e.stopPropagation()}>
        <div className="dialog-message">{message}</div>
        <div className="dialog-actions">
          <button className="dialog-btn secondary" onClick={onCancel}>
            取消
          </button>
          <button className="dialog-btn danger" onClick={onConfirm}>
            删除
          </button>
        </div>
      </div>
    </div>
  );
}
