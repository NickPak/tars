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
  const workspaceVisible = useLayoutStore((s) => s.workspaceVisible);

  useEffect(() => {
    const cleanup = useChatStore.getState().init();
    return cleanup;
  }, []);

  const gridClass = [
    "app",
    sidebarCollapsed ? "sidebar-collapsed" : "",
    workspaceVisible ? "" : "workspace-hidden",
  ]
    .filter(Boolean)
    .join(" ");

  return (
    <div className={gridClass}>
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
