import { useEffect, useState } from "react";
import { Window } from "@wailsio/runtime";
import { agentApi } from "../services/agentApi";

/**
 * 素材库导入门（素材浏览窗口专用）。
 *
 * 一键安装链路：站点上点 "Add to Excalidraw" → 站点把本窗口重定向到
 * <应用origin>/#addLibrary=<url>&token=... → 本组件在启动早期接管
 * （main.tsx 在渲染完整 App 前分流到这里）→ 后端代抓合并落盘 →
 * 广播事件刷新主窗口画板 → 自动关窗。
 */
export default function LibraryImportGate() {
  const [status, setStatus] = useState("正在导入素材库…");

  useEffect(() => {
    const m = /#addLibrary=([^&]+)/.exec(window.location.hash);
    if (!m) {
      setStatus("链接无效");
      return;
    }
    agentApi
      .importCanvasLibrary(decodeURIComponent(m[1]))
      .then(({ added }) => {
        setStatus(added > 0 ? `已导入 ${added} 个素材` : "素材已存在，无需导入");
        // 主窗口经事件即时刷新；本窗口使命完成，自动关闭
        setTimeout(() => void Window.Close(), 1200);
      })
      .catch((err) => {
        setStatus(`导入失败：${err instanceof Error ? err.message : String(err)}`);
      });
  }, []);

  return (
    <div className="library-import-gate">
      <div className="library-import-gate-text">{status}</div>
      <button className="dialog-btn secondary" onClick={() => void Window.Close()}>
        关闭窗口
      </button>
    </div>
  );
}
