import { useEffect, useRef, useState } from "react";
import { X } from "lucide-react";
import { useImageContextMenu } from "./ImageContextMenu";

const MIN_SCALE = 0.2;
const MAX_SCALE = 8;

/** 图片预览 lightbox：遮罩点击 / Esc / 关闭按钮退出；
 *  滚轮缩放（0.2x–8x），左键按住拖拽平移；重新打开时复位。 */
export default function ImagePreview({
  src,
  onClose,
}: {
  src: string | null;
  onClose: () => void;
}) {
  const [scale, setScale] = useState(1);
  const [offset, setOffset] = useState({ x: 0, y: 0 });
  const [dragging, setDragging] = useState(false);
  const imgRef = useRef<HTMLImageElement>(null);
  const stageRef = useRef<HTMLDivElement>(null);
  const { openMenu, menuEl } = useImageContextMenu();

  // 换图/重开时复位视图
  useEffect(() => {
    setScale(1);
    setOffset({ x: 0, y: 0 });
  }, [src]);

  // Esc 关闭
  useEffect(() => {
    if (!src) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") onClose();
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [src, onClose]);

  // 滚轮缩放：React 的 onWheel 是 passive 监听，preventDefault 无效，
  // 需手动以 non-passive 绑定。
  useEffect(() => {
    const img = imgRef.current;
    if (!img || !src) return;
    const onWheel = (e: WheelEvent) => {
      e.preventDefault();
      setScale((s) =>
        Math.min(MAX_SCALE, Math.max(MIN_SCALE, e.deltaY < 0 ? s * 1.15 : s / 1.15)),
      );
    };
    img.addEventListener("wheel", onWheel, { passive: false });
    return () => img.removeEventListener("wheel", onWheel);
  }, [src]);

  const onMouseDown = (e: React.MouseEvent) => {
    e.preventDefault();
    const start = { x: e.clientX, y: e.clientY, base: offset };
    setDragging(true);
    const move = (ev: MouseEvent) => {
      setOffset({
        x: start.base.x + ev.clientX - start.x,
        y: start.base.y + ev.clientY - start.y,
      });
    };
    const up = () => {
      setDragging(false);
      window.removeEventListener("mousemove", move);
      window.removeEventListener("mouseup", up);
    };
    window.addEventListener("mousemove", move);
    window.addEventListener("mouseup", up);
  };

  if (!src) return null;

  return (
    <div className="dialog-overlay" onClick={onClose}>
      {/* stage 包裹图片与关闭按钮：transform 作用在 stage 上，
          按钮随图片一起缩放/平移，始终贴近图片右上角 */}
      <div
        ref={stageRef}
        className="image-preview-stage"
        style={{
          transform: `translate(${offset.x}px, ${offset.y}px) scale(${scale})`,
          cursor: dragging ? "grabbing" : "grab",
        }}
        onClick={(e) => e.stopPropagation()}
        onMouseDown={onMouseDown}
        onContextMenu={(e) => openMenu(e, src)}
      >
        <button
          className="image-preview-close"
          title="关闭"
          aria-label="关闭预览"
          onClick={(e) => {
            e.stopPropagation();
            onClose();
          }}
        >
          <X size={16} />
        </button>
        <img
          ref={imgRef}
          className="image-preview"
          src={src}
          alt="图片预览"
          draggable={false}
        />
      </div>
      {menuEl}
    </div>
  );
}
