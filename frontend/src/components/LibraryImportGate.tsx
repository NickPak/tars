import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
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
  const { t } = useTranslation();
  const [status, setStatus] = useState(() => t("canvas.importing"));

  useEffect(() => {
    const m = /#addLibrary=([^&]+)/.exec(window.location.hash);
    if (!m) {
      setStatus(t("gate.invalid"));
      return;
    }
    agentApi
      .importCanvasLibrary(decodeURIComponent(m[1]))
      .then(({ added }) => {
        setStatus(
          added > 0 ? t("canvas.imported", { count: added }) : t("canvas.importExists"),
        );
        // 主窗口经事件即时刷新；本窗口使命完成，自动关闭
        setTimeout(() => void Window.Close(), 1200);
      })
      .catch((err) => {
        setStatus(
          t("canvas.importFailed", { msg: err instanceof Error ? err.message : String(err) }),
        );
      });
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  return (
    <div className="library-import-gate">
      <div className="library-import-gate-text">{status}</div>
      <button className="dialog-btn secondary" onClick={() => void Window.Close()}>
        {t("gate.closeWindow")}
      </button>
    </div>
  );
}
