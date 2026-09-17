import { useEffect, useRef, useState } from "react";
import { Check, Copy, Save } from "lucide-react";
import { agentApi } from "../services/agentApi";
import { useChatStore } from "../store/chatStore";

/** 把 data URL 图片复制到系统剪贴板。
 *  ClipboardItem 在 Chromium 系只保证支持 image/png，故经 canvas
 *  统一转码为 PNG（截图/jpg/webp 均可复制）。 */
async function copyImageToClipboard(dataUrl: string): Promise<void> {
  const img = new Image();
  img.src = dataUrl;
  await img.decode();
  const canvas = document.createElement("canvas");
  canvas.width = img.naturalWidth;
  canvas.height = img.naturalHeight;
  const ctx = canvas.getContext("2d");
  if (!ctx) throw new Error("canvas unavailable");
  ctx.drawImage(img, 0, 0);
  const blob = await new Promise<Blob>((resolve, reject) =>
    canvas.toBlob((b) => (b ? resolve(b) : reject(new Error("toBlob failed"))), "image/png"),
  );
  await navigator.clipboard.write([new ClipboardItem({ "image/png": blob })]);
}

interface MenuState {
  x: number;
  y: number;
  src: string;
}

/**
 * 图片右键菜单（复制/保存）的状态钩子：
 * 在 <img> 上 onContextMenu={(e) => openMenu(e, src)} 触发，
 * 把返回的 menuEl 渲染到组件树即可。点击他处 / Esc / 滚动自动关闭。
 */
export function useImageContextMenu() {
  const [menu, setMenu] = useState<MenuState | null>(null);
  const [copied, setCopied] = useState(false);
  const menuRef = useRef<HTMLDivElement>(null);
  const setBackendError = useChatStore((s) => s.setBackendError);

  const openMenu = (e: React.MouseEvent, src: string) => {
    e.preventDefault();
    e.stopPropagation();
    setCopied(false);
    setMenu({ x: e.clientX, y: e.clientY, src });
  };

  useEffect(() => {
    if (!menu) return;
    const close = () => setMenu(null);
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") close();
    };
    window.addEventListener("mousedown", close);
    window.addEventListener("keydown", onKey);
    window.addEventListener("blur", close);
    return () => {
      window.removeEventListener("mousedown", close);
      window.removeEventListener("keydown", onKey);
      window.removeEventListener("blur", close);
    };
  }, [menu]);

  const onCopy = async () => {
    if (!menu) return;
    try {
      await copyImageToClipboard(menu.src);
      setCopied(true);
      setTimeout(() => setMenu(null), 600);
    } catch (err) {
      setBackendError(`复制图片失败：${err instanceof Error ? err.message : String(err)}`);
      setMenu(null);
    }
  };

  const onSave = async () => {
    if (!menu) return;
    try {
      await agentApi.saveImage(menu.src); // 后端弹保存对话框；取消为空操作
    } catch (err) {
      setBackendError(`保存图片失败：${err instanceof Error ? err.message : String(err)}`);
    }
    setMenu(null);
  };

  // 视口边界钳制，防止菜单超出屏幕
  const style: React.CSSProperties = menu
    ? {
        left: Math.min(menu.x, window.innerWidth - 140),
        top: Math.min(menu.y, window.innerHeight - 90),
      }
    : {};

  const menuEl = menu ? (
    <div
      ref={menuRef}
      className="image-ctx-menu"
      style={style}
      // mousedown 防"点击他处关闭菜单"，click 防冒泡到预览遮罩触发其关闭
      onMouseDown={(e) => e.stopPropagation()}
      onClick={(e) => e.stopPropagation()}
    >
      <button className="image-ctx-item" onClick={() => void onCopy()}>
        {copied ? <Check size={14} /> : <Copy size={14} />}
        {copied ? "已复制" : "复制图片"}
      </button>
      <button className="image-ctx-item" onClick={() => void onSave()}>
        <Save size={14} />
        保存图片…
      </button>
    </div>
  ) : null;

  return { openMenu, menuEl };
}
