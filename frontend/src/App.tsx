import { useEffect } from "react";
import Sidebar from "./components/Sidebar";
import MessageList from "./components/MessageList";
import ChatInput from "./components/ChatInput";
import { useChatStore } from "./store/chatStore";

export default function App() {
  const backendError = useChatStore((s) => s.backendError);
  const dismissError = useChatStore((s) => s.dismissError);

  useEffect(() => {
    const cleanup = useChatStore.getState().init();
    return cleanup;
  }, []);

  return (
    <div className="app">
      <Sidebar />
      <main className="main">
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
    </div>
  );
}
