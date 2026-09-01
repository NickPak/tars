import { useState } from "react";
import {
  ChevronDown,
  MoreVertical,
  Hash,
  FolderOpen,
  ExternalLink,
  Copy,
  Check,
} from "lucide-react";
import { useChatStore } from "../store/chatStore";
import { agentApi } from "../services/agentApi";

/**
 * 对话标题栏：显示当前会话 ID（可复制，用于链路追踪检索）、工作区路径（锁定后只读）、模型选择器、更多操作。
 * 工作目录仅可在新建会话的欢迎页选择；会话一旦开始就锁定，这里只提供"在文件管理器中打开"。
 */
export default function TopicBar() {
  const activeId = useChatStore((s) => s.activeId);
  const workspace = useChatStore((s) => s.workspace);
  const model = useChatStore((s) => s.model);

  const [menuOpen, setMenuOpen] = useState(false);
  const [wsMenuOpen, setWsMenuOpen] = useState(false);
  const [modelMenuOpen, setModelMenuOpen] = useState(false);
  const [idCopied, setIdCopied] = useState(false);

  const models = useChatStore((s) => s.models);
  const setActiveModel = useChatStore((s) => s.setActiveModel);

  // 复制完整会话 ID，用于在链路追踪（OTel/Jaeger）中按 ID 检索对话
  const handleCopyId = () => {
    if (!activeId) return;
    navigator.clipboard.writeText(activeId).then(() => {
      setIdCopied(true);
      setTimeout(() => setIdCopied(false), 1500);
    });
  };

  const handleReveal = () => {
    if (activeId) void agentApi.revealInExplorer(activeId);
    setWsMenuOpen(false);
  };

  // 导出对话为 Markdown。保存对话框本身就是明确的成功反馈，
  // 成功与用户取消都静默，仅失败时提示。
  const setBackendError = useChatStore((s) => s.setBackendError);
  const handleExport = async () => {
    setMenuOpen(false);
    if (!activeId) return;
    try {
      await agentApi.exportSession(activeId);
    } catch (e) {
      setBackendError(`导出失败: ${e instanceof Error ? e.message : String(e)}`);
    }
  };

  return (
    <div className="topicbar">
      <div className="topicbar-title">
        <Hash size={15} className="topicbar-icon" />
        {activeId ? (
          <>
            <span
              className="topicbar-name topicbar-conv-id"
              title={`会话 ID：${activeId}\n用于链路追踪检索`}
            >
              {activeId}
            </span>
            <button
              className="topicbar-copy-btn"
              title="复制会话 ID"
              onClick={handleCopyId}
            >
              {idCopied ? <Check size={13} /> : <Copy size={13} />}
            </button>
          </>
        ) : (
          <span className="topicbar-name">新对话</span>
        )}
      </div>

      <div className="topicbar-actions">
        {/* 工作区路径按钮：无活动会话时不可点——此时还没有可打开的目录
            （会话首次发送才真正创建），点开只会是个空菜单 */}
        <button
          className="topicbar-ws-btn"
          disabled={!activeId}
          title={
            activeId
              ? workspace?.path || "未设置工作区"
              : "发送首条消息后可查看工作区"
          }
          onClick={() => setWsMenuOpen((v) => !v)}
        >
          <FolderOpen size={14} />
          <span className="topicbar-ws-name">
            {workspace?.name || "默认工作区"}
          </span>
          {activeId && <ChevronDown size={12} />}
        </button>

        {wsMenuOpen && activeId && (
          <>
            <div
              className="topicbar-menu-overlay"
              onClick={() => setWsMenuOpen(false)}
            />
            <div className="topicbar-menu">
              <button
                className="topicbar-menu-item"
                onClick={handleReveal}
              >
                <ExternalLink size={14} />
                在文件管理器中打开
              </button>
            </div>
          </>
        )}

        {/* 模型切换下拉（数据来自后端配置） */}
        {model?.modelId && (
          <button
            className="topicbar-model-btn"
            title={`模型：${model.entryId}\n上下文窗口：${model.contextWindow.toLocaleString()} tokens\n点击切换模型`}
            onClick={() => setModelMenuOpen((v) => !v)}
          >
            <span>{model.modelId}</span>
            <ChevronDown size={14} />
          </button>
        )}
        {modelMenuOpen && (
          <>
            <div
              className="topicbar-menu-overlay"
              onClick={() => setModelMenuOpen(false)}
            />
            <div className="topicbar-menu topicbar-model-menu">
              {models.map((m) => (
                <button
                  key={m.entryId}
                  className="topicbar-menu-item"
                  onClick={() => {
                    setModelMenuOpen(false);
                    if (!m.active) void setActiveModel(m.entryId);
                  }}
                >
                  <span
                    className={`topicbar-menu-check${m.active ? " active" : ""}`}
                  >
                    {m.active ? "✓" : ""}
                  </span>
                  <span className="topicbar-menu-label">{m.entryId}</span>
                </button>
              ))}
              {models.length === 0 && (
                <div className="topicbar-menu-empty">
                  暂无可用模型，请在设置中添加
                </div>
              )}
            </div>
          </>
        )}

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
              <button
                className="topicbar-menu-item"
                onClick={() => {
                  void handleExport();
                }}
              >
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
