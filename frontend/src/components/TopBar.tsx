import { Command, PanelRight } from "lucide-react";
import { useLayoutStore } from "../store/layoutStore";

/**
 * 顶部栏：窗口拖拽区 + 应用名 + 工作区切换按钮。
 * 使用 Wails 的 --wails-drag-frame 实现窗口拖拽。
 */
export default function TopBar() {
  const toggleWorkspace = useLayoutStore((s) => s.toggleWorkspace);
  const workspaceVisible = useLayoutStore((s) => s.workspaceVisible);

  return (
    <header className="topbar">
      <div className="topbar-drag" data-wails-drag>
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
