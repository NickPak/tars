import { useEffect, useRef, useState } from "react";
import { Plus, X, ChevronDown } from "lucide-react";
import { useChatStore } from "../store/chatStore";
import { agentApi } from "../services/agentApi";
import RenameDialog, { ConfirmDialog } from "./Dialog";

/**
 * 会话 Tab 条 —— 当前项目内"打开中"的全部会话（单项目多会话）：
 * 每个 Tab 是一个会话，共用项目工作区；末尾【+】在项目内新建会话。
 * 仅在激活项目时显示（无项目的空态不渲染）。
 *
 * 关闭 ≠ 删除：× 与右键"关闭"只是把 Tab 收进右侧【▾】已关闭列表
 * （数据保留，点击即重开）；只有右键"删除会话"才真正清空对话记录。
 */
export default function SessionTabs() {
  const activeProjectId = useChatStore((s) => s.activeProjectId);
  const activeId = useChatStore((s) => s.activeId);
  const sessions = useChatStore((s) => s.sessions);
  const selectSession = useChatStore((s) => s.selectSession);
  const addSessionTab = useChatStore((s) => s.addSessionTab);
  const closeSessionTab = useChatStore((s) => s.closeSessionTab);
  const reopenSessionTab = useChatStore((s) => s.reopenSessionTab);
  const deleteSessionTab = useChatStore((s) => s.deleteSessionTab);
  const closeOtherTabs = useChatStore((s) => s.closeOtherTabs);
  const closeAllTabs = useChatStore((s) => s.closeAllTabs);
  const renameSession = useChatStore((s) => s.renameSession);
  const setBackendError = useChatStore((s) => s.setBackendError);

  const [deleteTarget, setDeleteTarget] = useState<string | null>(null);
  const [renameTarget, setRenameTarget] = useState<{ id: string; title: string } | null>(null);
  const [ctxMenu, setCtxMenu] = useState<{ x: number; y: number; id: string } | null>(null);
  const [closedMenuPos, setClosedMenuPos] = useState<{ x: number; y: number } | null>(null);
  const ctxMenuRef = useRef<HTMLDivElement>(null);
  const closedMenuRef = useRef<HTMLDivElement>(null);

  // 点击外部 / Esc 关闭右键菜单与已关闭列表
  useEffect(() => {
    if (!ctxMenu && !closedMenuPos) return;
    const onClick = (e: MouseEvent) => {
      if (ctxMenuRef.current && !ctxMenuRef.current.contains(e.target as Node)) {
        setCtxMenu(null);
      }
      if (closedMenuRef.current && !closedMenuRef.current.contains(e.target as Node)) {
        setClosedMenuPos(null);
      }
    };
    const onEsc = (e: KeyboardEvent) => {
      if (e.key === "Escape") {
        setCtxMenu(null);
        setClosedMenuPos(null);
      }
    };
    document.addEventListener("mousedown", onClick);
    document.addEventListener("keydown", onEsc);
    return () => {
      document.removeEventListener("mousedown", onClick);
      document.removeEventListener("keydown", onEsc);
    };
  }, [ctxMenu, closedMenuPos]);

  if (!activeProjectId) return null;

  const projectSessions = sessions
    .filter((c) => c.projectId === activeProjectId)
    .sort((a, b) => a.createdAt - b.createdAt);
  const tabs = projectSessions.filter((t) => !t.closed);
  const closedTabs = projectSessions.filter((t) => t.closed);

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
            title="关闭会话（对话记录保留，可从右侧已关闭列表重开）"
            onClick={(e) => {
              e.stopPropagation();
              void closeSessionTab(t.id);
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

      {/* 已关闭会话列表（重开入口；始终可见，空列表禁用）。
          菜单用 fixed + 按钮视口坐标：.session-tabs 有 overflow-x，
          absolute 定位会被纵向裁剪；菜单保持在 closedMenuRef 的 DOM
          子树内（contains 判断按 DOM 而非视觉），点击外部关闭不受影响。 */}
      <div ref={closedMenuRef}>
        <button
          className="session-tab-add"
          title={closedTabs.length > 0 ? `已关闭的会话（${closedTabs.length}）` : "没有已关闭的会话"}
          aria-label="已关闭的会话"
          disabled={closedTabs.length === 0}
          onClick={(e) => {
            if (closedMenuPos) {
              setClosedMenuPos(null);
            } else {
              const r = e.currentTarget.getBoundingClientRect();
              // 菜单栏式下拉：菜单左缘对齐按钮左缘（向右展开）。
              // 不用 translateX 偏移：ctx-menu-in 动画播放期间会覆盖内联
              // transform，导致菜单位置闪烁。
              setClosedMenuPos({ x: r.left, y: r.bottom + 4 });
            }
          }}
        >
          <ChevronDown size={14} />
        </button>
        {closedMenuPos && closedTabs.length > 0 && (
          <div
            className="ws-ctx-menu"
            style={{ top: closedMenuPos.y, left: closedMenuPos.x }}
          >
            {closedTabs.map((t) => (
              <button
                key={t.id}
                className="ws-ctx-item"
                onClick={() => {
                  setClosedMenuPos(null);
                  void reopenSessionTab(t.id);
                }}
              >
                {t.title || "新对话"}
              </button>
            ))}
          </div>
        )}
      </div>

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
              void closeSessionTab(ctxTab.id);
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
          <div className="ws-ctx-separator" />
          <button
            className="ws-ctx-item"
            onClick={() => {
              setCtxMenu(null);
              setDeleteTarget(ctxTab.id);
            }}
          >
            删除会话
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

      {/* 删除 = 清空对话记录，不可撤销，需确认 */}
      <ConfirmDialog
        open={deleteTarget !== null}
        message="删除会话将清空其对话记录，此操作不可撤销。确定删除？"
        onCancel={() => setDeleteTarget(null)}
        onConfirm={() => {
          if (deleteTarget) void deleteSessionTab(deleteTarget);
          setDeleteTarget(null);
        }}
      />
    </div>
  );
}
