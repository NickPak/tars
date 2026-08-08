import { useState } from "react";
import {
  ChevronDown,
  MoreVertical,
  Hash,
  FolderOpen,
  RotateCcw,
  FolderInput,
  ExternalLink,
} from "lucide-react";
import { useChatStore } from "../store/chatStore";
import { agentApi } from "../services/agentApi";

/**
 * 对话标题栏：显示当前对话标题、工作区路径（可切换）、模型选择器、更多操作。
 */
export default function TopicBar() {
  const conversations = useChatStore((s) => s.conversations);
  const activeId = useChatStore((s) => s.activeId);
  const workspace = useChatStore((s) => s.workspace);
  const pickAndSetWorkspace = useChatStore((s) => s.pickAndSetWorkspace);
  const resetWorkspace = useChatStore((s) => s.resetWorkspace);

  const [menuOpen, setMenuOpen] = useState(false);
  const [wsMenuOpen, setWsMenuOpen] = useState(false);

  const activeConv = conversations.find((c) => c.id === activeId);
  const title = activeConv?.title || "新对话";

  const handleReveal = () => {
    if (activeId) void agentApi.revealInExplorer(activeId);
    setWsMenuOpen(false);
  };

  return (
    <div className="topicbar">
      <div className="topicbar-title">
        <Hash size={15} className="topicbar-icon" />
        <span className="topicbar-name">{title}</span>
      </div>

      <div className="topicbar-actions">
        {/* 工作区路径按钮 */}
        <button
          className="topicbar-ws-btn"
          title={workspace?.path || "未设置工作区"}
          onClick={() => setWsMenuOpen((v) => !v)}
        >
          <FolderOpen size={14} className={workspace?.isCustom ? "ws-custom" : ""} />
          <span className="topicbar-ws-name">
            {workspace?.name || "默认工作区"}
          </span>
          {workspace?.isCustom && <span className="ws-custom-badge">自定义</span>}
          <ChevronDown size={12} />
        </button>

        {wsMenuOpen && (
          <>
            <div
              className="topicbar-menu-overlay"
              onClick={() => setWsMenuOpen(false)}
            />
            <div className="topicbar-menu">
              <button
                className="topicbar-menu-item"
                onClick={() => {
                  void pickAndSetWorkspace();
                  setWsMenuOpen(false);
                }}
              >
                <FolderInput size={14} />
                打开目录…
              </button>
              {workspace?.isCustom && (
                <button
                  className="topicbar-menu-item"
                  onClick={() => {
                    void resetWorkspace();
                    setWsMenuOpen(false);
                  }}
                >
                  <RotateCcw size={14} />
                  重置为默认工作区
                </button>
              )}
              {activeId && (
                <button
                  className="topicbar-menu-item"
                  onClick={handleReveal}
                >
                  <ExternalLink size={14} />
                  在文件管理器中打开
                </button>
              )}
            </div>
          </>
        )}

        {/* 模型选择器 */}
        <button className="topicbar-model-btn" title="选择模型">
          <span>DeepSeek-R1</span>
          <ChevronDown size={14} />
        </button>

        {/* 更多操作 */}
        <button
          className="topicbar-btn"
          title="更多操作"
          aria-label="更多操作"
          onClick={() => setMenuOpen((v) => !v)}
        >
          <MoreVertical size={16} />
        </button>
        {menuOpen && (
          <>
            <div
              className="topicbar-menu-overlay"
              onClick={() => setMenuOpen(false)}
            />
            <div className="topicbar-menu">
              <button className="topicbar-menu-item" onClick={() => setMenuOpen(false)}>
                导出对话
              </button>
              <button className="topicbar-menu-item" onClick={() => setMenuOpen(false)}>
                清空消息
              </button>
              <button className="topicbar-menu-item" onClick={() => setMenuOpen(false)}>
                分享
              </button>
            </div>
          </>
        )}
      </div>
    </div>
  );
}
