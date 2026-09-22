import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import App from "./App";
import LibraryImportGate from "./components/LibraryImportGate";
import "./styles/app.css";

// 素材库浏览窗口的重定向着陆点：站点 "Add to Excalidraw" 会跳到
// <应用origin>/#addLibrary=<url>&token=...——检测到该 hash 即说明
// 本窗口是素材浏览窗口，渲染极简导入门而非完整应用。
const isLibraryImport = /#addLibrary=/.test(window.location.hash);

createRoot(document.getElementById("root") as HTMLElement).render(
  <StrictMode>{isLibraryImport ? <LibraryImportGate /> : <App />}</StrictMode>,
);
