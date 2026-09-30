import { lazy, Suspense, useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { Events, Window, Screens } from "@wailsio/runtime";
import { BookOpen, FileText, X, ArrowDownToLine, Save, SquareArrowOutUpRight } from "lucide-react";
import { agentApi } from "../services/agentApi";
import WindowControls from "./WindowControls";
import type { AuxTab } from "../types";

const CanvasBoard = lazy(() => import("./CanvasBoard"));
const FileViewer = lazy(() => import("./FileViewer"));

interface AuxSnapshot {
  tabs: AuxTab[];
  activeId: string;
  detached: AuxTab[];
}

/**
 * 副面板窗口（?view=aux）：画板 + 文件编辑器的统一容器，Tab 页形态。
 * Tab 列表由后端持有（CanvasService auxState），窗口关闭重开不丢；
 * 经 aux:tabs-changed 事件同步。Tab 内容保活（display:none 隐藏而非
 * 卸载），Monaco/Excalidraw 的编辑状态不丢。
 *
 * 拖出与吸回：
 * - Tab 向下拖出（拖离 Tab 条 >40px）成为独立窗口（?view=aux&detached=id）；
 * - 独立窗口拖回：拖动停止后与副窗口重叠 >25% 后端自动吸回，或点吸回按钮。
 */
export default function AuxWindow() {
  const { t } = useTranslation();
  const [snap, setSnap] = useState<AuxSnapshot>({ tabs: [], activeId: "", detached: [] });
  const [activeOverride, setActiveOverride] = useState<string | null>(null);
  const [supportsImages, setSupportsImages] = useState(false);

  // detached 模式：本窗口只承载一个 Tab（拖出的独立小窗口）
  const detachedId = new URLSearchParams(window.location.search).get("detached");

  useEffect(() => {
    void agentApi.getAuxTabs().then((s) => setSnap(s as AuxSnapshot)).catch(() => {});
    const off = Events.On("aux:tabs-changed", (ev) => setSnap(ev.data as AuxSnapshot));
    return () => off();
  }, []);

  useEffect(() => {
    void agentApi
      .getModelInfo()
      .then((m) => setSupportsImages(m.supportsImages ?? false))
      .catch(() => setSupportsImages(false));
  }, []);

  const tabs = detachedId ? snap.detached.filter((t) => t.id === detachedId) : snap.tabs;
  const activeId = activeOverride ?? snap.activeId;

  // Tab 拖拽（VSCode 风格）：HTML5 原生拖拽 + setDragImage 浮动芯片——
  // 拖拽图像由 OS 层渲染，拖出窗口外依然可见（web 内容无法画出窗口
  // 边界，这是唯一可行路径）；dragend 自带屏幕坐标，无需换算窗口原点。
  const [draggingId, setDraggingId] = useState<string | null>(null);

  const onTabDragStart = (t: AuxTab, e: React.DragEvent) => {
    // 离屏渲染芯片元素作为拖拽图像（OS 取位图快照，随后即可移除）
    const chip = document.createElement("div");
    chip.className = "aux-drag-chip";
    chip.style.position = "fixed";
    chip.style.left = "-9999px";
    chip.textContent = t.title;
    document.body.appendChild(chip);
    e.dataTransfer.setDragImage(chip, 20, 16);
    e.dataTransfer.effectAllowed = "move";
    e.dataTransfer.setData("text/plain", t.id); // 部分引擎要求非空数据才起拖
    setDraggingId(t.id);
    requestAnimationFrame(() => chip.remove());
  };

  const onTabDragEnd = (t: AuxTab, e: React.DragEvent) => {
    setDraggingId(null);
    const sx = e.screenX;
    const sy = e.screenY;
    if (detachedId) {
      void agentApi.dropDetachedTab(t.id, sx, sy); // 落点在副面板内则吸回
      return;
    }
    // 落在 Tab 条内 = 不拖出（留在原地）；之外 = 拖出为独立窗口
    void Window.Position().then((pos) => {
      const tabbarBottom = pos.y + 36;
      if (sy > tabbarBottom) {
        void agentApi.detachAuxTab(t.id, sx, sy);
      }
    });
  };

  // 原生拖拽默认在"非放置目标"上显示禁止光标；全局 dragover
  // preventDefault 让光标保持正常移动态
  useEffect(() => {
    const allow = (e: DragEvent) => e.preventDefault();
    window.addEventListener("dragover", allow, true);
    return () => window.removeEventListener("dragover", allow, true);
  }, []);




  const closeTab = (id: string) => void agentApi.closeAuxTab(id);

  // Tab 条双击：高度拉满 → 宽度拉满 → 还原快照 的三态循环。
  // 快照（尺寸+位置）仅在"完全还原态"（宽高都未满）双击时更新；
  // 判定带 4px 容差（DPI/边框补偿）；满高时 Y 归工作区顶、满宽时
  // X 归左缘，否则窗口会溢出屏幕；还原时位置也回快照（满宽步把
  // 窗口移到了左缘，"位置不动"会停在左边）。
  const restoreRef = useRef<{ w: number; h: number; x: number; y: number } | null>(null);
  const onTabbarDoubleClick = async (e: React.MouseEvent) => {
    if ((e.target as HTMLElement).closest(".aux-tab, button")) return;
    const TOL = 4;
    const [size, pos, scr] = await Promise.all([
      Window.Size(),
      Window.Position(),
      Screens.GetCurrent(),
    ]);
    const wa = scr.WorkArea;
    const fullH = size.height >= wa.Height - TOL;
    const fullW = size.width >= wa.Width - TOL;
    if (!fullH) {
      if (!fullW) restoreRef.current = { w: size.width, h: size.height, x: pos.x, y: pos.y };
      await Window.SetSize(size.width, wa.Height);
      await Window.SetPosition(pos.x, wa.Y);
    } else if (!fullW) {
      await Window.SetSize(wa.Width, wa.Height);
      await Window.SetPosition(wa.X, wa.Y);
    } else {
      const r = restoreRef.current ?? { w: 720, h: 800, x: pos.x, y: pos.y };
      await Window.SetSize(r.w, r.h);
      await Window.SetPosition(r.x, r.y);
    }
  };

  // Tab 右键菜单：保存/外部打开/关闭（编辑器动作经窗口内 CustomEvent
  // 转发给 FileViewer——编辑器实例与脏状态都在那里，菜单只发意图）
  const [ctxMenu, setCtxMenu] = useState<{ tab: AuxTab; x: number; y: number } | null>(null);
  useEffect(() => {
    if (!ctxMenu) return;
    const dismiss = () => setCtxMenu(null);
    const onEsc = (e: KeyboardEvent) => {
      if (e.key === "Escape") dismiss();
    };
    document.addEventListener("mousedown", dismiss);
    document.addEventListener("keydown", onEsc);
    return () => {
      document.removeEventListener("mousedown", dismiss);
      document.removeEventListener("keydown", onEsc);
    };
  }, [ctxMenu]);

  const tabAction = (tab: AuxTab, action: "save" | "close") => {
    window.dispatchEvent(new CustomEvent("aux:tab-action", { detail: { id: tab.id, action } }));
  };

  const renderTab = (t: AuxTab) =>
    t.kind === "canvas" ? (
      <CanvasBoard
        canInsertImage={supportsImages}
        draftSid={t.sessionId}
        onInsert={(dataUrl) => {
          void agentApi.canvasInsertImage(dataUrl).finally(() => closeTab(t.id));
        }}
        onInsertMermaid={(mermaid) => {
          void agentApi.canvasInsertMermaid(mermaid).finally(() => closeTab(t.id));
        }}
      />
    ) : (
      <FileViewer
        sessionId={t.sessionId}
        path={t.path ?? ""}
        tabId={t.id}
        onClose={() => closeTab(t.id)}
      />
    );

  return (
    <div className="aux-window">
      {/* Tab 条兼任无边框标题栏（拖拽区 + 窗口控制 + 双击扩缩循环） */}
      <div className="aux-tabbar" onDoubleClick={(e) => void onTabbarDoubleClick(e)}>
        {tabs.map((tab) => (
          <div
            key={tab.id}
            className={`aux-tab${tab.id === activeId ? " active" : ""}${draggingId === tab.id ? " dragging" : ""}`}
            onClick={() => setActiveOverride(tab.id)}
            onMouseDown={(e) => {
              // 中键关闭标签（浏览器惯例）；preventDefault 阻止自动滚动。
              // 编辑器 Tab 走 aux:tab-action 通道（FileViewer 有脏检查），
              // 画板 Tab 无监听者，直接关闭。
              if (e.button === 1) {
                e.preventDefault();
                if (tab.kind === "editor") tabAction(tab, "close");
                else closeTab(tab.id);
              }
            }}
            onContextMenu={(e) => {
              e.preventDefault();
              setCtxMenu({ tab, x: e.clientX, y: e.clientY });
            }}
            draggable
            onDragStart={(e) => onTabDragStart(tab, e)}
            onDragEnd={(e) => onTabDragEnd(tab, e)}
            title={tab.kind === "editor" ? tab.path : undefined}
          >
            {/* 画板 Tab 用书本图标（与 Excalidraw 素材库图标同形态） */}
            {tab.kind === "canvas" ? <BookOpen size={13} /> : <FileText size={13} />}
            {/* 画板标题由后端写死中文，渲染时按界面语言覆盖；编辑器 Tab 用文件名 */}
            <span className="aux-tab-title">
              {tab.kind === "canvas" ? t("canvas.title") : tab.title}
            </span>
            <button
              className="aux-tab-close"
              title={t("aux.closeTab")}
              onClick={(e) => {
                e.stopPropagation();
                closeTab(tab.id);
              }}
            >
              <X size={12} />
            </button>
          </div>
        ))}
        <div className="aux-tabbar-spacer" />
        {/* 独立窗口 Tab 条显示吸回按钮（也可拖回副面板自动吸回） */}
        {detachedId && (
          <button
            className="topbar-btn"
            title={t("aux.reattach")}
            onClick={() => void agentApi.reattachAuxTab(detachedId)}
          >
            <ArrowDownToLine size={15} />
          </button>
        )}
        <WindowControls />
      </div>

      {/* Tab 内容保活：全部挂载，隐藏的仅 display:none */}
      <div className="aux-content">
        {tabs.map((tab) => (
          <div
            key={tab.id}
            className="aux-pane"
            style={{ display: tab.id === activeId ? undefined : "none" }}
          >
            <Suspense fallback={<div className="settings-loading">{t("aux.loading")}</div>}>
              {renderTab(tab)}
            </Suspense>
          </div>
        ))}
        {tabs.length === 0 && <div className="settings-loading">{t("aux.empty")}</div>}
      </div>

      {/* Tab 右键菜单（编辑器动作经 aux:tab-action 转发给 FileViewer） */}
      {ctxMenu && (
        <div
          className="ws-ctx-menu"
          style={{ top: ctxMenu.y, left: ctxMenu.x }}
          onMouseDown={(e) => e.stopPropagation()}
        >
          {ctxMenu.tab.kind === "editor" && (
            <>
              <button
                className="ws-ctx-item"
                onClick={() => {
                  tabAction(ctxMenu.tab, "save");
                  setCtxMenu(null);
                }}
              >
                <Save size={14} />
                {t("aux.save")}
              </button>
              <button
                className="ws-ctx-item"
                onClick={() => {
                  void agentApi.openFile(ctxMenu.tab.sessionId, ctxMenu.tab.path ?? "");
                  setCtxMenu(null);
                }}
              >
                <SquareArrowOutUpRight size={14} />
                {t("workspace.openDefault")}
              </button>
            </>
          )}
          <button
            className="ws-ctx-item"
            onClick={() => {
              tabAction(ctxMenu.tab, "close"); // FileViewer 内有脏检查
              setCtxMenu(null);
            }}
          >
            <X size={14} />
            {t("aux.closeTab")}
          </button>
        </div>
      )}
    </div>
  );
}
