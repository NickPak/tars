import { Command, PanelLeft, PanelRight } from "lucide-react";
import { useLayoutStore } from "../store/layoutStore";

/**
 * 顶部栏：窗口拖拽区 + 应用名 + 侧边栏/工作区切换按钮。
 * 侧边栏折叠后（其内部的收起按钮随面板一起隐藏），
 * 展开入口露出于此，保证随时可以恢复。
 */
export default function TopBar() {
  const toggleWorkspace = useLayoutStore((s) => s.toggleWorkspace);
  const workspaceVisible = useLayoutStore((s) => s.workspaceVisible);
  const sidebarCollapsed = useLayoutStore((s) => s.sidebarCollapsed);
  const toggleSidebar = useLayoutStore((s) => s.toggleSidebar);

  return (
    <header className="topbar">
      <div className="topbar-drag" data-wails-drag>
        {sidebarCollapsed && (
          <button
            className="topbar-btn"
            title="展开侧边栏"
            aria-label="展开侧边栏"
            onClick={toggleSidebar}
          >
            <PanelLeft size={16} />
          </button>
        )}
        <span className="topbar-brand">TARS</span>
      </div>
      <div className="topbar-actions">
        <button
          className="topbar-btn"
          title="命令面板 (Ctrl+K)"
          aria-label="命令面板"
        >
          <Command size={16} />
        </button>
        <button
          className={`topbar-btn${workspaceVisible ? " active" : ""}`}
          title="切换工作区面板"
          aria-label="切换工作区面板"
          onClick={toggleWorkspace}
        >
          <PanelRight size={16} />
        </button>
      </div>
    </header>
  );
}
