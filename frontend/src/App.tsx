import { useEffect } from "react";
import Sidebar from "./components/Sidebar";
import MessageList from "./components/MessageList";
import ChatInput from "./components/ChatInput";
import TopBar from "./components/TopBar";
import TopicBar from "./components/TopicBar";
import WorkspacePanel from "./components/WorkspacePanel";
import StatusBar from "./components/StatusBar";
import { useChatStore } from "./store/chatStore";
import { useLayoutStore } from "./store/layoutStore";

export default function App() {
  const backendError = useChatStore((s) => s.backendError);
  const dismissError = useChatStore((s) => s.dismissError);
  const sidebarCollapsed = useLayoutStore((s) => s.sidebarCollapsed);
  const sidebarWidth = useLayoutStore((s) => s.sidebarWidth);
  const workspaceVisible = useLayoutStore((s) => s.workspaceVisible);
  const workspaceWidth = useLayoutStore((s) => s.workspaceWidth);

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
      <Sidebar />
      <main className="chat-pane">
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
      <StatusBar />
    </div>
  );
}
