import { Window } from "@wailsio/runtime";
import WindowControls from "./WindowControls";

/**
 * 顶部栏：窗口拖拽区 + 应用名。
 * （项目列表/项目面板的开关分别在左右两侧的常驻图标栏。）
 *
 * Windows 无边框模式下兼任自定义标题栏：整条栏为拖拽区
 * （CSS --wails-draggable: drag），双击切换最大化；右端为窗口
 * 控制按钮（WindowControls）；macOS 用原生红绿灯，不渲染。
 */
export default function TopBar() {
  return (
    <header className="topbar">
      {/* 双击拖拽区切换最大化（JS 拖拽方案下没有原生 NC 双击，手动补） */}
      <div
        className="topbar-drag"
        onDoubleClick={(e) => {
          if ((e.target as HTMLElement).closest(".topbar-btn")) return;
          void Window.ToggleMaximise();
        }}
      >
        <span className="topbar-brand">TARS</span>
      </div>
      <div className="topbar-actions">
        <WindowControls />
      </div>
    </header>
  );
}
