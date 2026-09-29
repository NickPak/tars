import { useEffect, useState } from "react";
import { Minus, Square, Copy, X } from "lucide-react";
import { System, Window, Events } from "@wailsio/runtime";

/**
 * 无边框窗口的系统控制按钮（最小化/最大化·还原/关闭），仅 Windows 渲染
 * （macOS 用原生红绿灯）。
 *
 * 平台判定走异步 Environment()：同步的 IsWindows() 读取注入全局
 * window._wails，首帧渲染时注入可能尚未完成。
 * onClose 可覆盖默认的 Window.Close()（如编辑器窗口需要脏检查确认）。
 */
export default function WindowControls({ onClose }: { onClose?: () => void }) {
  const [isWindows, setIsWindows] = useState(false);
  const [maximised, setMaximised] = useState(false);

  useEffect(() => {
    void System.Environment().then((env) => setIsWindows(env.OS === "windows"));
  }, []);

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

  if (!isWindows) return null;

  return (
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
        onClick={() => (onClose ? onClose() : void Window.Close())}
      >
        <X size={16} />
      </button>
    </>
  );
}
