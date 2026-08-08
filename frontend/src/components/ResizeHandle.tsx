import { useCallback } from "react";

interface ResizeHandleProps {
  /** 把手位于面板的哪一侧：right（侧边栏，右拖变宽）| left（工作区，左拖变宽） */
  side: "left" | "right";
  /** 当前面板宽度 */
  width: number;
  /** 宽度回调（store 内部已 clamp + 持久化） */
  onResize: (width: number) => void;
}

/**
 * 面板边缘拖拽把手：mousedown 后监听全局 mousemove/mouseup。
 * 拖拽期间给 body 加 resizing-col，全局禁用文本选择并统一光标。
 */
export default function ResizeHandle({ side, width, onResize }: ResizeHandleProps) {
  const handleMouseDown = useCallback(
    (e: React.MouseEvent) => {
      e.preventDefault();
      const startX = e.clientX;
      const startWidth = width; // 按下时刻的宽度，整个拖拽过程以此为基准

      const onMove = (ev: MouseEvent) => {
        const delta = ev.clientX - startX;
        // 把手在右（侧边栏）：向右拖变宽；把手在左（工作区）：向左拖变宽
        onResize(side === "right" ? startWidth + delta : startWidth - delta);
      };
      const onUp = () => {
        document.removeEventListener("mousemove", onMove);
        document.removeEventListener("mouseup", onUp);
        document.body.classList.remove("resizing-col");
      };
      document.addEventListener("mousemove", onMove);
      document.addEventListener("mouseup", onUp);
      document.body.classList.add("resizing-col");
    },
    [side, width, onResize],
  );

  return (
    <div
      className={`resize-handle resize-handle-${side}`}
      onMouseDown={handleMouseDown}
      role="separator"
      aria-orientation="vertical"
    />
  );
}
