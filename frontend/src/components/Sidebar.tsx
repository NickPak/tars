import { useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { Plus, Pencil, Trash2 } from "lucide-react";
import { useChatStore } from "../store/chatStore";
import { useLayoutStore } from "../store/layoutStore";
import { agentApi } from "../services/agentApi";
import RenameDialog, { ConfirmDialog } from "./Dialog";
import ResizeHandle from "./ResizeHandle";

export default function Sidebar() {
  const { t } = useTranslation();
  const projects = useChatStore((s) => s.projects);
  const sessions = useChatStore((s) => s.sessions);
  const activeProjectId = useChatStore((s) => s.activeProjectId);
  const newSession = useChatStore((s) => s.newSession);
  const selectProject = useChatStore((s) => s.selectProject);
  const deleteProject = useChatStore((s) => s.deleteProject);
  const renameProject = useChatStore((s) => s.renameProject);
  const setBackendError = useChatStore((s) => s.setBackendError);
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
        title: p.title || latest?.title || t("sidebar.newProject"),
        sessionCount: ss.length,
        workspace: p.workspaceDir || t("sidebar.defaultWorkspace"),
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
      .catch((e) =>
        setBackendError(t("common.copyFailed", { msg: e instanceof Error ? e.message : String(e) })),
      );
    setCtxMenu(null);
  };

  const handleRevealWorkspace = (projectId: string) => {
    setCtxMenu(null);
    agentApi
      .revealProjectWorkspace(projectId)
      .catch((e) =>
        setBackendError(
          t("sidebar.revealFailed", { msg: e instanceof Error ? e.message : String(e) }),
        ),
      );
  };

  return (
    <aside className="sidebar">
      {/* 收起/展开开关在左侧常驻图标栏（App.tsx 的 left-rail） */}
      <button className="new-chat-btn" onClick={newSession}>
        <Plus size={16} />
        {t("sidebar.newProject")}
      </button>

      <div className="sidebar-section">{t("sidebar.recent")}</div>

      <nav className="session-list">
        {items.length === 0 && (
          <div className="session-empty">{t("sidebar.empty")}</div>
        )}
        {items.map((c) => (
          <div
            key={c.projectId}
            className={`session-item${c.projectId === activeProjectId ? " active" : ""}`}
            title={t("sidebar.itemTooltip", { title: c.title, workspace: c.workspace })}
            onClick={() => void selectProject(c.projectId)}
            onContextMenu={(e) => {
              e.preventDefault();
              setCtxMenu({ x: e.clientX, y: e.clientY, projectId: c.projectId });
            }}
          >
            <span className="session-title">{c.title}</span>
            <span className="session-actions">
              <button
                aria-label={t("common.rename")}
                title={t("common.rename")}
                onClick={(e) => {
                  e.stopPropagation();
                  setRenameTarget({ id: c.projectId, title: c.title });
                }}
              >
                <Pencil size={14} />
              </button>
              <button
                aria-label={t("common.delete")}
                title={t("common.delete")}
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

      {/* 设置入口固定在左侧常驻图标栏底部（App.tsx 的 left-rail） */}

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
            {t("common.rename")}
          </button>
          <button className="ws-ctx-item" onClick={() => handleCopyId(ctxItem.projectId)}>
            {t("sidebar.copyProjectId")}
          </button>
          <button className="ws-ctx-item" onClick={() => handleRevealWorkspace(ctxItem.projectId)}>
            {t("sidebar.revealWorkspace")}
          </button>
          <div className="ws-ctx-separator" />
          <button
            className="ws-ctx-item"
            onClick={() => {
              setCtxMenu(null);
              setDeleteTarget(ctxItem.projectId);
            }}
          >
            {t("sidebar.deleteProject")}
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
          const item = items.find((i) => i.projectId === deleteTarget);
          return t("sidebar.deleteConfirm", {
            title: item?.title ?? "",
            count: item?.sessionCount ?? 0,
          });
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
