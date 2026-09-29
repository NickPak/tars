import { useEffect } from "react";
import { Folder, ListTree, Settings } from "lucide-react";
import Sidebar from "./components/Sidebar";
import MessageList from "./components/MessageList";
import ChatInput from "./components/ChatInput";
import TopBar from "./components/TopBar";
import TopicBar from "./components/TopicBar";
import SessionTabs from "./components/SessionTabs";
import WorkspacePanel from "./components/WorkspacePanel";
import StatusBar from "./components/StatusBar";
import SettingsPanel from "./components/SettingsPanel";
import { useChatStore } from "./store/chatStore";
import { useLayoutStore } from "./store/layoutStore";
import { useSettingsStore } from "./store/settingsStore";

export default function App() {
  const backendError = useChatStore((s) => s.backendError);
  const dismissError = useChatStore((s) => s.dismissError);
  const sidebarCollapsed = useLayoutStore((s) => s.sidebarCollapsed);
  const sidebarWidth = useLayoutStore((s) => s.sidebarWidth);
  const workspaceVisible = useLayoutStore((s) => s.workspaceVisible);
  const workspaceWidth = useLayoutStore((s) => s.workspaceWidth);
  const toggleWorkspace = useLayoutStore((s) => s.toggleWorkspace);
  const toggleSidebar = useLayoutStore((s) => s.toggleSidebar);

  useEffect(() => {
    const cleanup = useChatStore.getState().init();
    return cleanup;
  }, []);

  // 禁用 WebView 默认右键菜单（前进/后退/刷新/检查等），
  // 但放行输入区域（input/textarea/contenteditable）以保留复制粘贴菜单。
  // 文件树的自定义右键菜单通过 stopPropagation 拦截，不会到这里。
  useEffect(() => {
    const handler = (e: MouseEvent) => {
      const target = e.target as HTMLElement | null;
      if (target?.closest('input, textarea, [contenteditable="true"]')) return;
      e.preventDefault();
    };
    document.addEventListener("contextmenu", handler);
    return () => document.removeEventListener("contextmenu", handler);
  }, []);

  // Ctrl+, 打开设置（常见桌面应用约定）
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.ctrlKey && e.key === ",") {
        e.preventDefault();
        useSettingsStore.getState().openSettings();
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, []);



  const gridClass = [
    "app",
    sidebarCollapsed ? "sidebar-collapsed" : "",
    workspaceVisible ? "" : "workspace-hidden",
  ]
    .filter(Boolean)
    .join(" ");

  return (
    <div
      className={gridClass}
      style={
        {
          "--sidebar-width": `${sidebarWidth}px`,
          "--workspace-width": `${workspaceWidth}px`,
        } as React.CSSProperties
      }
    >
      <TopBar />
      {/* 左侧常驻图标栏：项目列表（文件夹图标）等左侧功能的开关入口 */}
      <aside className="left-rail">
        <button
          className={`rail-btn${sidebarCollapsed ? "" : " active"}`}
          title={sidebarCollapsed ? "展开项目列表" : "收起项目列表"}
          aria-label={sidebarCollapsed ? "展开项目列表" : "收起项目列表"}
          onClick={toggleSidebar}
        >
          <Folder size={17} />
        </button>
        {/* 底部固定区：设置等全局入口（sidebar 折叠后也始终可达） */}
        <div className="rail-bottom">
          <button
            className="rail-btn"
            title="设置 (Ctrl+,)"
            aria-label="设置"
            onClick={() => useSettingsStore.getState().openSettings()}
          >
            <Settings size={17} />
          </button>
        </div>
      </aside>
      <Sidebar />
      <main className="chat-pane">
        <SessionTabs />
        <TopicBar />
        {backendError && (
          <div className="backend-error" role="alert">
            <span>后端调用失败:{backendError}</span>
            <button onClick={dismissError} aria-label="关闭">
              ✕
            </button>
          </div>
        )}
        <MessageList />
        <ChatInput />
      </main>
      {workspaceVisible && <WorkspacePanel />}
      {/* 文件编辑器是独立 OS 窗口（FileService.OpenEditorWindow），不在主窗口网格内 */}
      {/* 右侧常驻图标栏：工作区等右侧功能的开关入口 */}
      <aside className="right-rail">
        <button
          className={`rail-btn${workspaceVisible ? " active" : ""}`}
          title={workspaceVisible ? "收起工作区" : "展开工作区"}
          aria-label={workspaceVisible ? "收起工作区" : "展开工作区"}
          onClick={toggleWorkspace}
        >
          <ListTree size={17} />
        </button>
      </aside>
      <StatusBar />
      <SettingsPanel />
    </div>
  );
}
