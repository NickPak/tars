import { useEffect, useState } from "react";
import { Window } from "@wailsio/runtime";
import CanvasBoard from "./CanvasBoard";
import { agentApi } from "../services/agentApi";

/**
 * 画板独立窗口的极简视图（main.tsx 在 ?view=canvas 时渲染本组件，
 * 不加载完整 App）。
 *
 * 与主窗口的协作：插入产出经后端事件（canvas:insert-image /
 * canvas:insert-mermaid）广播给主窗口的 ChatInput 插入输入框；
 * 完成后本窗口自动关闭。模型能力（图片门控）自行拉取——
 * chatStore 是每窗口独立状态，不能依赖主窗口。
 */
export default function CanvasWindow() {
  const [supportsImages, setSupportsImages] = useState(false);

  useEffect(() => {
    void agentApi
      .getModelInfo()
      .then((m) => setSupportsImages(m.supportsImages ?? false))
      .catch(() => setSupportsImages(false));
  }, []);

  const closeWindow = () => void Window.Close();

  return (
    <div className="canvas-window">
      <CanvasBoard
        canInsertImage={supportsImages}
        onInsert={(dataUrl) => {
          void agentApi.canvasInsertImage(dataUrl).finally(closeWindow);
        }}
        onInsertMermaid={(mermaid) => {
          void agentApi.canvasInsertMermaid(mermaid).finally(closeWindow);
        }}
        onClose={closeWindow}
      />
    </div>
  );
}
