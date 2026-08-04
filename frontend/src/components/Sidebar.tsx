import { useState } from "react";
import { useChatStore } from "../store/chatStore";
import RenameDialog, { ConfirmDialog } from "./Dialog";

export default function Sidebar() {
  const conversations = useChatStore((s) => s.conversations);
  const activeId = useChatStore((s) => s.activeId);
  const newConversation = useChatStore((s) => s.newConversation);
  const selectConversation = useChatStore((s) => s.selectConversation);
  const deleteConversation = useChatStore((s) => s.deleteConversation);
  const renameConversation = useChatStore((s) => s.renameConversation);

  // 重命名对话框状态
  const [renameTarget, setRenameTarget] = useState<{
    id: string;
    title: string;
  } | null>(null);

  // 删除确认对话框状态
  const [deleteTarget, setDeleteTarget] = useState<string | null>(null);

  return (
    <aside className="sidebar">
      <div className="sidebar-header">
        <span className="sidebar-brand">TARS</span>
      </div>

      <button className="new-chat-btn" onClick={newConversation}>
        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round">
          <line x1="12" y1="5" x2="12" y2="19" />
          <line x1="5" y1="12" x2="19" y2="12" />
        </svg>
        新对话
      </button>

      <div className="sidebar-section">最近</div>

      <nav className="conversation-list">
        {conversations.length === 0 && (
          <div className="conversation-empty">暂无历史对话</div>
        )}
        {conversations.map((c) => (
          <div
            key={c.id}
            className={`conversation-item${c.id === activeId ? " active" : ""}`}
            onClick={() => void selectConversation(c.id)}
          >
            <span className="conversation-title">{c.title || "新对话"}</span>
            <span className="conversation-actions">
              <button
                aria-label="重命名"
                title="重命名"
                onClick={(e) => {
                  e.stopPropagation();
                  setRenameTarget({ id: c.id, title: c.title });
                }}
              >
                <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                  <path d="M17 3a2.828 2.828 0 1 1 4 4L7.5 20.5 2 22l1.5-5.5L17 3z" />
                </svg>
              </button>
              <button
                aria-label="删除"
                title="删除"
                onClick={(e) => {
                  e.stopPropagation();
                  setDeleteTarget(c.id);
                }}
              >
                <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                  <polyline points="3 6 5 6 21 6" />
                  <path d="M19 6v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6m3 0V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2" />
                </svg>
              </button>
            </span>
          </div>
        ))}
      </nav>

      {/* 重命名对话框 */}
      <RenameDialog
        open={renameTarget !== null}
        oldTitle={renameTarget?.title ?? ""}
        onCancel={() => setRenameTarget(null)}
        onConfirm={(title) => {
          if (renameTarget && title !== renameTarget.title) {
            void renameConversation(renameTarget.id, title);
          }
          setRenameTarget(null);
        }}
      />

      {/* 删除确认对话框 */}
      <ConfirmDialog
        open={deleteTarget !== null}
        message="确定删除该对话？此操作不可撤销。"
        onCancel={() => setDeleteTarget(null)}
        onConfirm={() => {
          if (deleteTarget) {
            void deleteConversation(deleteTarget);
          }
          setDeleteTarget(null);
        }}
      />
    </aside>
  );
}
