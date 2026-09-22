import { useEffect, useState } from "react";
import { Minus, Square, Copy, X } from "lucide-react";
import { System, Window, Events } from "@wailsio/runtime";

/**
 * 顶部栏：窗口拖拽区 + 应用名。
 * （项目列表/项目面板的开关分别在左右两侧的常驻图标栏。）
 *
 * Windows 无边框模式下兼任自定义标题栏：整条栏为拖拽区
 * （CSS app-region: drag，双击切换最大化），右端渲染
 * 最小化/最大化/关闭按钮；macOS 用原生红绿灯，不渲染。
 */
export default function TopBar() {

  // 平台判定走异步 Environment()：同步的 IsWindows() 读取注入全局
  // window._wails，首帧渲染时注入可能尚未完成，导致按钮不渲染。
  const [isWindows, setIsWindows] = useState(false);
  useEffect(() => {
    void System.Environment().then((env) => setIsWindows(env.OS === "windows"));
  }, []);
  const [maximised, setMaximised] = useState(false);

  useEffect(() => {
    if (!isWindows) return;
    void Window.IsMaximised().then(setMaximised);
    // 双击拖拽区等系统级最大化路径也要同步图标
    const offMax = Events.On("windows:WindowMaximise", () => setMaximised(true));
    const offUnmax = Events.On("windows:WindowUnMaximise", () => setMaximised(false));
    return () => {
      offMax();
      offUnmax();
    };
  }, [isWindows]);

  return (
    <header className="topbar">
      <div className="topbar-drag">
        <span className="topbar-brand">TARS</span>
      </div>
      <div className="topbar-actions">
        {isWindows && (
          <>
            <span className="topbar-win-divider" />
            <button
              className="topbar-btn"
              title="最小化"
              aria-label="最小化"
              onClick={() => void Window.Minimise()}
            >
              <Minus size={15} />
            </button>
            <button
              className="topbar-btn"
              title={maximised ? "还原" : "最大化"}
              aria-label={maximised ? "还原" : "最大化"}
              onClick={() => {
                void Window.ToggleMaximise();
                setMaximised((v) => !v);
              }}
            >
              {maximised ? <Copy size={13} /> : <Square size={13} />}
            </button>
            <button
              className="topbar-btn topbar-btn-close"
              title="关闭"
              aria-label="关闭"
              onClick={() => void Window.Close()}
            >
              <X size={16} />
            </button>
          </>
        )}
      </div>
    </header>
  );
}
