import { useEffect, useRef, useState } from "react";
import { Plus, Pencil, Trash2, Settings, PanelLeftClose } from "lucide-react";
import { useChatStore } from "../store/chatStore";
import { useLayoutStore } from "../store/layoutStore";
import { useSettingsStore } from "../store/settingsStore";
import { agentApi } from "../services/agentApi";
import RenameDialog, { ConfirmDialog } from "./Dialog";
import ResizeHandle from "./ResizeHandle";

export default function Sidebar() {
  const projects = useChatStore((s) => s.projects);
  const sessions = useChatStore((s) => s.sessions);
  const activeProjectId = useChatStore((s) => s.activeProjectId);
  const newSession = useChatStore((s) => s.newSession);
  const selectProject = useChatStore((s) => s.selectProject);
  const deleteProject = useChatStore((s) => s.deleteProject);
  const renameProject = useChatStore((s) => s.renameProject);
  const setBackendError = useChatStore((s) => s.setBackendError);
  const toggleSidebar = useLayoutStore((s) => s.toggleSidebar);
  const sidebarWidth = useLayoutStore((s) => s.sidebarWidth);
  const setSidebarWidth = useLayoutStore((s) => s.setSidebarWidth);

  // 项目条目展示：显式标题优先，否则取项目内最近会话的自动命名；
  // 排序按最近活跃
  const items = projects
    .map((p) => {
      const ss = sessions
        .filter((c) => c.projectId === p.id)
        .sort((a, b) => a.createdAt - b.createdAt);
      const latest = ss[ss.length - 1];
      return {
        projectId: p.id,
        title: p.title || latest?.title || "新项目",
        sessionCount: ss.length,
        workspace: p.workspaceDir || "默认工作区",
        updatedAt: ss.reduce((m, c) => Math.max(m, c.updatedAt), p.updatedAt),
      };
    })
    .sort((a, b) => b.updatedAt - a.updatedAt);

  const [renameTarget, setRenameTarget] = useState<{
    id: string;
    title: string;
  } | null>(null);

  const [deleteTarget, setDeleteTarget] = useState<string | null>(null);
  const [ctxMenu, setCtxMenu] = useState<{ x: number; y: number; projectId: string } | null>(null);
  const ctxMenuRef = useRef<HTMLDivElement>(null);

  // 点击外部 / Esc 关闭右键菜单
  useEffect(() => {
    if (!ctxMenu) return;
    const onClick = (e: MouseEvent) => {
      if (ctxMenuRef.current && !ctxMenuRef.current.contains(e.target as Node)) {
        setCtxMenu(null);
      }
    };
    const onEsc = (e: KeyboardEvent) => {
      if (e.key === "Escape") setCtxMenu(null);
    };
    document.addEventListener("mousedown", onClick);
    document.addEventListener("keydown", onEsc);
    return () => {
      document.removeEventListener("mousedown", onClick);
      document.removeEventListener("keydown", onEsc);
    };
  }, [ctxMenu]);

  const ctxItem = items.find((i) => i.projectId === ctxMenu?.projectId);

  const handleCopyId = (id: string) => {
    navigator.clipboard
      .writeText(id)
      .catch((e) => setBackendError(`复制失败: ${e instanceof Error ? e.message : String(e)}`));
    setCtxMenu(null);
  };

  const handleRevealWorkspace = (projectId: string) => {
    setCtxMenu(null);
    agentApi
      .revealProjectWorkspace(projectId)
      .catch((e) => setBackendError(`打开工作区失败: ${e instanceof Error ? e.message : String(e)}`));
  };

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
        新项目
      </button>

      <div className="sidebar-section">最近</div>

      <nav className="session-list">
        {items.length === 0 && (
          <div className="session-empty">暂无项目</div>
        )}
        {items.map((c) => (
          <div
            key={c.projectId}
            className={`session-item${c.projectId === activeProjectId ? " active" : ""}`}
            title={`${c.title}\n工作区：${c.workspace}`}
            onClick={() => void selectProject(c.projectId)}
            onContextMenu={(e) => {
              e.preventDefault();
              setCtxMenu({ x: e.clientX, y: e.clientY, projectId: c.projectId });
            }}
          >
            <span className="session-title">{c.title}</span>
            <span className="session-actions">
              <button
                aria-label="重命名"
                title="重命名"
                onClick={(e) => {
                  e.stopPropagation();
                  setRenameTarget({ id: c.projectId, title: c.title });
                }}
              >
                <Pencil size={14} />
              </button>
              <button
                aria-label="删除"
                title="删除"
                onClick={(e) => {
                  e.stopPropagation();
                  setDeleteTarget(c.projectId);
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

      {/* 项目右键菜单（与会话 Tab 菜单同款交互） */}
      {ctxMenu && ctxItem && (
        <div
          ref={ctxMenuRef}
          className="ws-ctx-menu"
          style={{ top: ctxMenu.y, left: ctxMenu.x }}
        >
          <button
            className="ws-ctx-item"
            onClick={() => {
              setRenameTarget({ id: ctxItem.projectId, title: ctxItem.title });
              setCtxMenu(null);
            }}
          >
            重命名
          </button>
          <button className="ws-ctx-item" onClick={() => handleCopyId(ctxItem.projectId)}>
            复制项目 ID
          </button>
          <button className="ws-ctx-item" onClick={() => handleRevealWorkspace(ctxItem.projectId)}>
            在文件管理器中打开工作区
          </button>
          <div className="ws-ctx-separator" />
          <button
            className="ws-ctx-item"
            onClick={() => {
              setCtxMenu(null);
              setDeleteTarget(ctxItem.projectId);
            }}
          >
            删除项目
          </button>
        </div>
      )}

      {/* 重命名对话框（项目级：显式命名后不再跟随会话自动命名） */}
      <RenameDialog
        open={renameTarget !== null}
        oldTitle={renameTarget?.title ?? ""}
        onCancel={() => setRenameTarget(null)}
        onConfirm={(title) => {
          if (renameTarget && title !== renameTarget.title) {
            void renameProject(renameTarget.id, title);
          }
          setRenameTarget(null);
        }}
      />

      {/* 删除确认对话框（级联其下全部会话） */}
      <ConfirmDialog
        open={deleteTarget !== null}
        message={(() => {
          const t = items.find((i) => i.projectId === deleteTarget);
          return `确定删除项目「${t?.title ?? ""}」及其 ${t?.sessionCount ?? 0} 个会话？此操作不可撤销。`;
        })()}
        onCancel={() => setDeleteTarget(null)}
        onConfirm={() => {
          if (deleteTarget) {
            void deleteProject(deleteTarget);
          }
          setDeleteTarget(null);
        }}
      />

      {/* 右边缘拖拽把手（调整侧边栏宽度） */}
      <ResizeHandle side="right" width={sidebarWidth} onResize={setSidebarWidth} />
    </aside>
  );
}
