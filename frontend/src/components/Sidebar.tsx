import { useState } from "react";
import { Plus, Pencil, Trash2, Settings, PanelLeftClose } from "lucide-react";
import { useChatStore } from "../store/chatStore";
import { useLayoutStore } from "../store/layoutStore";
import RenameDialog, { ConfirmDialog } from "./Dialog";

export default function Sidebar() {
  const conversations = useChatStore((s) => s.conversations);
  const activeId = useChatStore((s) => s.activeId);
  const newConversation = useChatStore((s) => s.newConversation);
  const selectConversation = useChatStore((s) => s.selectConversation);
  const deleteConversation = useChatStore((s) => s.deleteConversation);
  const renameConversation = useChatStore((s) => s.renameConversation);
  const toggleSidebar = useLayoutStore((s) => s.toggleSidebar);

  const [renameTarget, setRenameTarget] = useState<{
    id: string;
    title: string;
  } | null>(null);

  const [deleteTarget, setDeleteTarget] = useState<string | null>(null);

  return (
    <aside className="sidebar">
      <div className="sidebar-header">
        <button
          className="sidebar-collapse-btn"
          onClick={toggleSidebar}
          title="收起侧边栏"
          aria-label="收起侧边栏"
        >
          <PanelLeftClose size={18} />
        </button>
      </div>

      <button className="new-chat-btn" onClick={newConversation}>
        <Plus size={16} />
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
                <Pencil size={14} />
              </button>
              <button
                aria-label="删除"
                title="删除"
                onClick={(e) => {
                  e.stopPropagation();
                  setDeleteTarget(c.id);
                }}
              >
                <Trash2 size={14} />
              </button>
            </span>
          </div>
        ))}
      </nav>

      <div className="sidebar-footer">
        <button className="sidebar-settings-btn" title="设置">
          <Settings size={16} />
          <span>设置</span>
        </button>
      </div>

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
