import { useEffect, useRef, useState } from "react";
import { Plus, X } from "lucide-react";
import { useChatStore } from "../store/chatStore";
import { agentApi } from "../services/agentApi";
import RenameDialog, { ConfirmDialog } from "./Dialog";

/**
 * 会话 Tab 条 —— 当前项目内的全部会话（单项目多会话）：
 * 每个 Tab 是一个会话，共用项目工作区；末尾【+】在项目内新建会话。
 * 仅在激活项目时显示（无项目的空态不渲染）。
 * 右键菜单：重命名 / 复制会话 ID / 导出对话 / 关闭 / 关闭其他 / 关闭全部。
 */
export default function SessionTabs() {
  const activeProjectId = useChatStore((s) => s.activeProjectId);
  const activeId = useChatStore((s) => s.activeId);
  const sessions = useChatStore((s) => s.sessions);
  const selectSession = useChatStore((s) => s.selectSession);
  const addSessionTab = useChatStore((s) => s.addSessionTab);
  const closeSessionTab = useChatStore((s) => s.closeSessionTab);
  const closeOtherTabs = useChatStore((s) => s.closeOtherTabs);
  const closeAllTabs = useChatStore((s) => s.closeAllTabs);
  const renameSession = useChatStore((s) => s.renameSession);
  const setBackendError = useChatStore((s) => s.setBackendError);

  const [closeTarget, setCloseTarget] = useState<string | null>(null);
  const [renameTarget, setRenameTarget] = useState<{ id: string; title: string } | null>(null);
  const [ctxMenu, setCtxMenu] = useState<{ x: number; y: number; id: string } | null>(null);
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

  if (!activeProjectId) return null;

  const tabs = sessions
    .filter((c) => c.projectId === activeProjectId)
    .sort((a, b) => a.createdAt - b.createdAt);

  const ctxTab = tabs.find((t) => t.id === ctxMenu?.id);

  const handleCopyId = (id: string) => {
    navigator.clipboard
      .writeText(id)
      .catch((e) => setBackendError(`复制失败: ${e instanceof Error ? e.message : String(e)}`));
    setCtxMenu(null);
  };

  const handleExport = (id: string) => {
    setCtxMenu(null);
    agentApi
      .exportSession(id)
      .catch((e) => setBackendError(`导出失败: ${e instanceof Error ? e.message : String(e)}`));
  };

  return (
    <div className="session-tabs">
      {tabs.map((t) => (
        <div
          key={t.id}
          className={`session-tab${t.id === activeId ? " active" : ""}`}
          title={t.title || "新对话"}
          onClick={() => void selectSession(t.id)}
          onContextMenu={(e) => {
            e.preventDefault();
            e.stopPropagation();
            setCtxMenu({ x: e.clientX, y: e.clientY, id: t.id });
          }}
        >
          <span className="session-tab-title">{t.title || "新对话"}</span>
          <button
            className="session-tab-close"
            aria-label="关闭会话"
            title="关闭会话（删除该会话的对话记录）"
            onClick={(e) => {
              e.stopPropagation();
              setCloseTarget(t.id);
            }}
          >
            <X size={12} />
          </button>
        </div>
      ))}
      <button
        className="session-tab-add"
        title="新建会话（与当前项目共用工作区）"
        aria-label="新建会话"
        onClick={() => void addSessionTab()}
      >
        <Plus size={14} />
      </button>

      {/* 右键菜单（复用工作区面板的菜单样式） */}
      {ctxMenu && ctxTab && (
        <div
          ref={ctxMenuRef}
          className="ws-ctx-menu"
          style={{ top: ctxMenu.y, left: ctxMenu.x }}
        >
          <button
            className="ws-ctx-item"
            onClick={() => {
              setRenameTarget({ id: ctxTab.id, title: ctxTab.title });
              setCtxMenu(null);
            }}
          >
            重命名
          </button>
          <button className="ws-ctx-item" onClick={() => handleCopyId(ctxTab.id)}>
            复制会话 ID
          </button>
          <button className="ws-ctx-item" onClick={() => handleExport(ctxTab.id)}>
            导出对话
          </button>
          <div className="ws-ctx-separator" />
          <button
            className="ws-ctx-item"
            onClick={() => {
              setCtxMenu(null);
              setCloseTarget(ctxTab.id);
            }}
          >
            关闭
          </button>
          <button
            className="ws-ctx-item"
            disabled={tabs.length <= 1}
            onClick={() => {
              setCtxMenu(null);
              void closeOtherTabs(ctxTab.id);
            }}
          >
            关闭其他标签
          </button>
          <button
            className="ws-ctx-item"
            onClick={() => {
              setCtxMenu(null);
              void closeAllTabs();
            }}
          >
            关闭全部标签
          </button>
        </div>
      )}

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

      {/* 关闭即删除对话记录，需确认 */}
      <ConfirmDialog
        open={closeTarget !== null}
        message="关闭会话将删除其对话记录，此操作不可撤销。确定关闭？"
        onCancel={() => setCloseTarget(null)}
        onConfirm={() => {
          if (closeTarget) void closeSessionTab(closeTarget);
          setCloseTarget(null);
        }}
      />
    </div>
  );
}
