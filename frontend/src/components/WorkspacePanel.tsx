import { useEffect, useState, useRef } from "react";
import {
  PanelRightClose,
  FileText,
  Folder,
  FolderOpen,
  Loader2,
  Inbox,
  SquareArrowOutUpRight,
  RefreshCw,
  ChevronsUp,
  ChevronsDown,
  ArrowDownAZ,
  ArrowDownZA,
  MoreHorizontal,
  ChevronRight,
} from "lucide-react";
import { useLayoutStore } from "../store/layoutStore";
import { useChatStore } from "../store/chatStore";
import { useWorkspaceStore } from "../store/workspaceStore";
import { agentApi, subscribeFileChanges } from "../services/agentApi";
import ResizeHandle from "./ResizeHandle";
import type { FileEntry } from "../types";

/**
 * 右侧工作区面板 —— 模仿 DeepSeek-Reasonix 的"项目"面板设计：
 *   - 顶部"项目"标题 + 右上角图标按钮（历史/刷新/折叠全部/更多/添加/收起）
 *   - 主体是简洁的文件树，单击展开/折叠，双击用系统默认程序打开
 *   - Agent 生成文件后自动刷新（监听工具执行事件，防抖 300ms）
 */
export default function WorkspacePanel() {
  const toggleWorkspace = useLayoutStore((s) => s.toggleWorkspace);
  const workspaceWidth = useLayoutStore((s) => s.workspaceWidth);
  const setWorkspaceWidth = useLayoutStore((s) => s.setWorkspaceWidth);
  const refresh = useWorkspaceStore((s) => s.refresh);
  const loading = useWorkspaceStore((s) => s.loading);
  const toggleExpandAll = useWorkspaceStore((s) => s.toggleExpandAll);
  const expandAll = useWorkspaceStore((s) => s.expandAll);
  const nameOrder = useWorkspaceStore((s) => s.nameOrder);
  const cycleNameOrder = useWorkspaceStore((s) => s.cycleNameOrder);

  const [moreOpen, setMoreOpen] = useState(false);
  const moreRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (!moreOpen) return;
    const onClick = (e: MouseEvent) => {
      if (moreRef.current && !moreRef.current.contains(e.target as Node)) {
        setMoreOpen(false);
      }
    };
    document.addEventListener("mousedown", onClick);
    return () => document.removeEventListener("mousedown", onClick);
  }, [moreOpen]);

  const activeId = useChatStore((s) => s.activeId);
  const handleRevealWorkspace = () => {
    if (!activeId) return;
    void agentApi.revealInExplorer(activeId);
    setMoreOpen(false);
  };

  return (
    <aside className="workspace">
      {/* 左边缘拖拽把手（调整工作区宽度） */}
      <ResizeHandle side="left" width={workspaceWidth} onResize={setWorkspaceWidth} />
      <div className="workspace-section-header">
        <span className="workspace-section-title">项目</span>
        <div className="workspace-section-actions">
          <button
            className="ws-icon-btn"
            title="刷新文件列表"
            onClick={() => void refresh()}
          >
            <RefreshCw size={16} className={loading ? "ws-spin" : ""} />
          </button>
          <button
            className="ws-icon-btn"
            title={expandAll ? "全部折叠" : "全部展开"}
            onClick={toggleExpandAll}
          >
            {expandAll ? <ChevronsUp size={16} /> : <ChevronsDown size={16} />}
          </button>
          <button
            className="ws-icon-btn"
            title={nameOrder === "asc" ? "名称排序：正序（点击切换为倒序）" : "名称排序：倒序（点击切换为正序）"}
            onClick={cycleNameOrder}
          >
            {nameOrder === "asc" ? <ArrowDownAZ size={16} /> : <ArrowDownZA size={16} />}
          </button>
          <div className="ws-more-wrapper" ref={moreRef}>
            <button
              className="ws-icon-btn"
              title="更多"
              onClick={() => setMoreOpen((v) => !v)}
            >
              <MoreHorizontal size={16} />
            </button>
            {moreOpen && (
              <div className="ws-more-menu">
                <button className="ws-ctx-item" onClick={handleRevealWorkspace}>
                  <FolderOpen size={14} />
                  在文件管理器中打开工作区
                </button>
              </div>
            )}
          </div>
          <button
            className="ws-icon-btn"
            title="收起面板"
            onClick={toggleWorkspace}
          >
            <PanelRightClose size={16} />
          </button>
        </div>
      </div>

      <div className="workspace-body">
        <FilesTab />
      </div>
    </aside>
  );
}

/** 收集树中所有目录路径（用于"全部展开"） */
function allDirPaths(entries: FileEntry[]): Set<string> {
  const out = new Set<string>();
  const walk = (list: FileEntry[]) => {
    for (const e of list) {
      if (e.isDir) {
        out.add(e.path);
        if (e.children) walk(e.children);
      }
    }
  };
  walk(entries);
  return out;
}

/** 文件树：状态来自 workspaceStore，支持自动刷新 */
function FilesTab() {
  const activeId = useChatStore((s) => s.activeId);
  // 工作区路径：切换自定义目录后必须重新加载文件树，
  // 否则面板继续显示旧目录的内容
  const workspacePath = useChatStore((s) => s.workspace?.path);
  const tree = useWorkspaceStore((s) => s.tree);
  const loading = useWorkspaceStore((s) => s.loading);
  const error = useWorkspaceStore((s) => s.error);
  const refresh = useWorkspaceStore((s) => s.refresh);
  const expandKey = useWorkspaceStore((s) => s.expandKey);
  const expandAllFlag = useWorkspaceStore((s) => s.expandAll);

  const [expanded, setExpanded] = useState<Set<string>>(new Set());
  // 标记是否已对当前（会话+工作区）做过"首次展开根目录"
  const initializedRef = useRef(false);
  // 展开/折叠目标状态的引用：切换会话的 effect 里要用最新值，
  // 又不能把它列进依赖（否则点 toggle 会误触发重新加载）
  const expandAllRef = useRef(expandAllFlag);
  expandAllRef.current = expandAllFlag;

  // 切换会话或切换工作区目录：重新加载 + 按全局 expandAll 状态初始化展开集合
  //（新目录结构与旧目录无关，必须重建 expanded）
  useEffect(() => {
    setExpanded(expandAllRef.current ? allDirPaths(useWorkspaceStore.getState().tree) : new Set());
    initializedRef.current = true; // 已在上方完成初始化，跳过后续"首次加载"逻辑
    void refresh();
  }, [activeId, workspacePath, refresh]);

  // 首次加载时默认展开根级目录（仅在切换 effect 之前树已有数据的场景兜底，
  // 例如应用启动后第一次选中会话；refresh 后的树更新不会走到这里）
  useEffect(() => {
    if (!initializedRef.current && tree.length > 0) {
      setExpanded(new Set(tree.filter((e) => e.isDir).map((e) => e.path)));
      initializedRef.current = true;
    }
  }, [tree]);

  // 全部展开/折叠（二合一开关）
  useEffect(() => {
    if (expandKey === 0) return;
    setExpanded(expandAllFlag ? allDirPaths(tree) : new Set());
  }, [expandKey]);

  // 自动刷新：Agent 工具执行完成 / 一轮回复结束时触发（防抖 300ms）
  useEffect(() => {
    if (!activeId) return;
    let timer: ReturnType<typeof setTimeout> | null = null;
    const unsubscribe = subscribeFileChanges(activeId, () => {
      if (timer) clearTimeout(timer);
      timer = setTimeout(() => void refresh(), 300);
    });
    return () => {
      if (timer) clearTimeout(timer);
      unsubscribe();
    };
  }, [activeId, refresh]);

  // 右键菜单状态
  const [ctxMenu, setCtxMenu] = useState<{
    x: number;
    y: number;
    entry: FileEntry;
  } | null>(null);
  const ctxMenuRef = useRef<HTMLDivElement>(null);

  // 点击外部关闭右键菜单
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

  const setBackendError = useChatStore((s) => s.setBackendError);

  const handleOpenFile = (relPath: string) => {
    if (!activeId) return;
    agentApi.openFile(activeId, relPath).catch((e) => {
      setBackendError(`打开文件失败: ${e instanceof Error ? e.message : String(e)}`);
    });
  };

  const handleRevealFile = (relPath: string) => {
    if (!activeId) return;
    agentApi.revealFileInExplorer(activeId, relPath).catch((e) => {
      setBackendError(`在文件管理器中显示失败: ${e instanceof Error ? e.message : String(e)}`);
    });
    setCtxMenu(null);
  };

  const handleContextMenu = (e: React.MouseEvent, entry: FileEntry) => {
    e.preventDefault();
    e.stopPropagation();
    setCtxMenu({ x: e.clientX, y: e.clientY, entry });
  };

  const toggleExpand = (path: string) => {
    setExpanded((prev) => {
      const next = new Set(prev);
      if (next.has(path)) {
        next.delete(path);
      } else {
        next.add(path);
      }
      return next;
    });
  };

  if (!activeId) {
    return (
      <div className="ws-empty">
        <Inbox size={32} className="ws-empty-icon" />
        <p>未选择会话</p>
        <span>选择一个对话后查看其工作区文件</span>
      </div>
    );
  }

  if (loading && tree.length === 0) {
    return (
      <div className="ws-loading">
        <Loader2 size={20} className="ws-loading-spinner" />
      </div>
    );
  }

  if (error) {
    return (
      <div className="ws-empty">
        <Inbox size={32} className="ws-empty-icon" />
        <p>加载失败</p>
        <span>{error}</span>
        <button className="ws-retry-btn" onClick={() => void refresh()}>
          <RefreshCw size={14} /> 重试
        </button>
      </div>
    );
  }

  if (tree.length === 0) {
    return (
      <div className="ws-empty">
        <Inbox size={32} className="ws-empty-icon" />
        <p>工作区为空</p>
        <span>Agent 生成的文件将显示在这里</span>
      </div>
    );
  }

  return (
    <div className="ws-files">
      <div className="ws-file-tree">
        {tree.map((entry) => (
          <FileTreeNode
            key={entry.path}
            entry={entry}
            depth={0}
            expanded={expanded}
            onToggle={toggleExpand}
            onOpenFile={handleOpenFile}
            onContextMenu={handleContextMenu}
          />
        ))}
      </div>

      {/* 右键上下文菜单 */}
      {ctxMenu && (
        <div
          ref={ctxMenuRef}
          className="ws-ctx-menu"
          style={{ top: ctxMenu.y, left: ctxMenu.x }}
        >
          {!ctxMenu.entry.isDir && (
            <button
              className="ws-ctx-item"
              onClick={() => {
                handleOpenFile(ctxMenu.entry.path);
                setCtxMenu(null);
              }}
            >
              <SquareArrowOutUpRight size={14} />
              用默认程序打开
            </button>
          )}
          <button
            className="ws-ctx-item"
            onClick={() => handleRevealFile(ctxMenu.entry.path)}
          >
            <FolderOpen size={14} />
            在文件资源管理器中显示
          </button>
        </div>
      )}
    </div>
  );
}

/** 递归渲染文件树节点 */
function FileTreeNode({
  entry,
  depth,
  expanded,
  onToggle,
  onOpenFile,
  onContextMenu,
}: {
  entry: FileEntry;
  depth: number;
  expanded: Set<string>;
  onToggle: (path: string) => void;
  onOpenFile: (relPath: string) => void;
  onContextMenu: (e: React.MouseEvent, entry: FileEntry) => void;
}) {
  const isExpanded = expanded.has(entry.path);

  if (entry.isDir) {
    return (
      <div className="ws-tree-node">
        <div
          className="ws-file-item ws-file-dir"
          style={{ paddingLeft: 8 + depth * 14 }}
          onClick={() => onToggle(entry.path)}
          onContextMenu={(e) => onContextMenu(e, entry)}
        >
          <ChevronRight
            size={13}
            className={`ws-file-chevron${isExpanded ? " expanded" : ""}`}
          />
          {isExpanded ? (
            <FolderOpen size={14} className="ws-file-icon" />
          ) : (
            <Folder size={14} className="ws-file-icon" />
          )}
          <span className="ws-file-name">{entry.name}</span>
        </div>
        {isExpanded && entry.children && (
          <div className="ws-tree-children">
            {entry.children.map((child) => (
              <FileTreeNode
                key={child.path}
                entry={child}
                depth={depth + 1}
                expanded={expanded}
                onToggle={onToggle}
                onOpenFile={onOpenFile}
                onContextMenu={onContextMenu}
              />
            ))}
          </div>
        )}
      </div>
    );
  }

  return (
    <div
      className="ws-file-item ws-file-leaf"
      style={{ paddingLeft: 8 + depth * 14 + 13 }}
      onDoubleClick={() => onOpenFile(entry.path)}
      onContextMenu={(e) => onContextMenu(e, entry)}
      title="双击用默认程序打开 · 右键更多操作"
    >
      <FileText size={13} className="ws-file-icon" />
      <span className="ws-file-name">{entry.name}</span>
    </div>
  );
}
