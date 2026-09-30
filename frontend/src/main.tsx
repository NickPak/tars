import { StrictMode, lazy, Suspense } from "react";
import { createRoot } from "react-dom/client";
import App from "./App";
import LibraryImportGate from "./components/LibraryImportGate";
import { initTheme } from "./theme/theme";
import { initI18n } from "./i18n";
import "./styles/app.css";

// 主题必须在渲染前应用（同步读 localStorage 翻转 data-theme，零闪烁）；
// 主窗口/副面板/导入门共用本入口，全部生效。
initTheme();
// i18n 同步资源初始化（渲染前完成，见文件底部）

// 素材库浏览窗口的重定向着陆点：站点 "Add to Excalidraw" 会跳到
// <应用origin>/#addLibrary=<url>&token=...——检测到该 hash 即说明
// 本窗口是素材浏览窗口，渲染极简导入门而非完整应用。
const isLibraryImport = /#addLibrary=/.test(window.location.hash);

// 副面板窗口：?view=aux 时渲染画板/编辑器的 Tab 容器（懒加载，
// excalidraw/monaco 不进主 bundle）。
const AuxWindow = lazy(() => import("./components/AuxWindow"));
const isAuxView = new URLSearchParams(window.location.search).get("view") === "aux";

const root = isLibraryImport ? (
  <LibraryImportGate />
) : isAuxView ? (
  <Suspense fallback={null}>
    <AuxWindow />
  </Suspense>
) : (
  <App />
);

// i18n 初始化完成后再渲染，避免首帧闪现键名（内联资源下基本是同步的，
// 这里只是兜底保险）
void initI18n().finally(() => {
  createRoot(document.getElementById("root") as HTMLElement).render(<StrictMode>{root}</StrictMode>);
});
