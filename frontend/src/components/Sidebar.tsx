import { useState } from "react";
import { Plus, Pencil, Trash2, Settings, PanelLeftClose } from "lucide-react";
import { useChatStore } from "../store/chatStore";
import { useLayoutStore } from "../store/layoutStore";
import { useSettingsStore } from "../store/settingsStore";
import RenameDialog, { ConfirmDialog } from "./Dialog";
import ResizeHandle from "./ResizeHandle";

export default function Sidebar() {
  const sessions = useChatStore((s) => s.sessions);
  const activeId = useChatStore((s) => s.activeId);
  const newSession = useChatStore((s) => s.newSession);
  const selectSession = useChatStore((s) => s.selectSession);
  const deleteSession = useChatStore((s) => s.deleteSession);
  const renameSession = useChatStore((s) => s.renameSession);
  const toggleSidebar = useLayoutStore((s) => s.toggleSidebar);
  const sidebarWidth = useLayoutStore((s) => s.sidebarWidth);
  const setSidebarWidth = useLayoutStore((s) => s.setSidebarWidth);

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

      <button className="new-chat-btn" onClick={newSession}>
        <Plus size={16} />
        新对话
      </button>

      <div className="sidebar-section">最近</div>

      <nav className="session-list">
        {sessions.length === 0 && (
          <div className="session-empty">暂无历史对话</div>
        )}
        {sessions.map((c) => (
          <div
            key={c.id}
            className={`session-item${c.id === activeId ? " active" : ""}`}
            onClick={() => void selectSession(c.id)}
          >
            <span className="session-title">{c.title || "新对话"}</span>
            <span className="session-actions">
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
        <button
          className="sidebar-settings-btn"
          title="设置 (Ctrl+,)"
          onClick={() => useSettingsStore.getState().openSettings()}
        >
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
            void renameSession(renameTarget.id, title);
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
            void deleteSession(deleteTarget);
          }
          setDeleteTarget(null);
        }}
      />

      {/* 右边缘拖拽把手（调整侧边栏宽度） */}
      <ResizeHandle side="right" width={sidebarWidth} onResize={setSidebarWidth} />
    </aside>
  );
}
