import { useEffect, useRef, useState } from "react";
import { Excalidraw, exportToBlob } from "@excalidraw/excalidraw";
import type { ExcalidrawImperativeAPI, ExcalidrawInitialDataState, LibraryItems } from "@excalidraw/excalidraw/types";
import "@excalidraw/excalidraw/index.css";
import { X, ImageDown, Workflow } from "lucide-react";
import { Events } from "@wailsio/runtime";
import { sceneToMermaid } from "../utils/excalidrawMermaid";
import type { ConvertResult } from "../utils/excalidrawMermaid";
import { agentApi } from "../services/agentApi";

/**
 * 画板弹层（Excalidraw，懒加载——本模块经 React.lazy 引入，
 * excalidraw 的 ~1MB 资源不进入主 bundle）。
 * 产出双通道：PNG 图片（多模态管道，受图片能力门控）/
 * Mermaid 代码块（scene→Mermaid 确定性转换，全模型可用）。
 */
export default function CanvasBoard({
  canInsertImage,
  onInsert,
  onInsertMermaid,
  onClose,
}: {
  /** 当前模型是否声明图片能力（false 时导出按钮禁用并提示） */
  canInsertImage: boolean;
  onInsert: (dataUrl: string) => void;
  onInsertMermaid: (mermaid: string) => void;
  onClose: () => void;
}) {
  const apiRef = useRef<ExcalidrawImperativeAPI | null>(null);
  const [exporting, setExporting] = useState(false);

  // 素材库持久化接管：Excalidraw 默认存 WebView localStorage（位置深、
  // 随缓存清理丢失）；改为 TARS 工作目录下的 canvas/library.json。
  // initialData 支持异步函数（装载磁盘素材）；onLibraryChange 变化后
  // 800ms 防抖写盘。
  const saveTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  useEffect(
    () => () => {
      if (saveTimerRef.current) clearTimeout(saveTimerRef.current);
    },
    [],
  );
  const loadLibrary = async (): Promise<ExcalidrawInitialDataState | null> => {
    const json = await agentApi.getCanvasLibrary().catch(() => "");
    if (!json) return null;
    try {
      return { libraryItems: JSON.parse(json) as LibraryItems };
    } catch {
      return null; // 磁盘素材损坏时按空库工作（不影响画板主流程）
    }
  };
  const onLibraryChange = (items: unknown) => {
    if (saveTimerRef.current) clearTimeout(saveTimerRef.current);
    saveTimerRef.current = setTimeout(() => {
      void agentApi.setCanvasLibrary(JSON.stringify(items));
    }, 800);
  };

  const [importNote, setImportNote] = useState<string | null>(null);

  // 素材库导入（#addLibrary hash 兜底路径使用；主链路是应用内素材
  // 浏览窗口 + 启动早期导入门 LibraryImportGate，不经由画板）。
  const importFrom = (sourceUrl: string) => {
    setImportNote("正在导入素材库…");
    agentApi
      .importCanvasLibrary(sourceUrl)
      .then(({ json, added }) => {
        if (apiRef.current && json) {
          void apiRef.current.updateLibrary({ libraryItems: JSON.parse(json) });
        }
        setImportNote(added > 0 ? `已导入 ${added} 个素材` : "素材已存在，无需导入");
        setTimeout(() => setImportNote(null), 3000);
      })
      .catch((err) => {
        setImportNote(`素材导入失败：${err instanceof Error ? err.message : String(err)}`);
        setTimeout(() => setImportNote(null), 5000);
      });
  };
  // 供 hashchange 回调拿到最新闭包
  const importFromRef = useRef(importFrom);
  importFromRef.current = importFrom;

  // 拦截素材面板的 "Browse libraries" 链接（Excalidraw 菜单经 portal
  // 渲染到 document.body，必须挂在 document 捕获阶段才能收到）：
  // 1) 默认 target=_blank 会在系统浏览器打开站点，安装跳回
  //    wails.localhost 不可达——改为应用内窗口打开；
  // 2) 站点按页面 URL 的 target 参数决定 "Add to Excalidraw" 的
  //    打开方式，改写 target=_self 使安装跳转发生在同一窗口——
  //    回到我们的 origin 时由启动早期导入门接管，一键完成导入。
  useEffect(() => {
    const onClick = (e: MouseEvent) => {
      const anchor = (e.target as HTMLElement).closest?.(
        'a[href*="libraries.excalidraw.com"]',
      ) as HTMLAnchorElement | null;
      if (!anchor) return;
      e.preventDefault();
      e.stopPropagation();
      const url = anchor.href.replace(/([?&])target=[^&]*/, "$1target=_self");
      void agentApi.openLibraryBrowser(url);
    };
    document.addEventListener("click", onClick, true);
    return () => document.removeEventListener("click", onClick, true);
  }, []);

  // 素材库被导入（本窗口或素材浏览窗口）后即时刷新素材面板
  useEffect(() => {
    const off = Events.On("canvas:library-changed", () => {
      void agentApi.getCanvasLibrary().then((json) => {
        if (apiRef.current && json) {
          void apiRef.current.updateLibrary({ libraryItems: JSON.parse(json) });
        }
      });
    });
    return () => off();
  }, []);

  // 兜底拦截 Excalidraw 网页版的 #addLibrary 跳转 hash（仅当跳转
  // 恰好发生在本 webview 内时有效——主链路是应用内素材浏览窗口 +
  // 启动早期导入门；手动粘贴入口也可随时用）。
  useEffect(() => {
    const tryImport = () => {
      const m = /#addLibrary=([^&]+)/.exec(window.location.hash);
      if (!m) return;
      history.replaceState(null, "", window.location.pathname + window.location.search);
      importFromRef.current(decodeURIComponent(m[1]));
    };
    tryImport();
    window.addEventListener("hashchange", tryImport);
    return () => window.removeEventListener("hashchange", tryImport);
  }, []);
  // 转换结果：最新值在 ref（点击时取用）；state 仅承载按钮状态，
  // 且仅在结果实质变化时更新——onChange 每次产生新数组，若直接
  // setState 会驱动 Excalidraw 重渲染回流 jotai store，造成
  // "Maximum update depth exceeded" 无限循环。
  const [conv, setConv] = useState<ConvertResult>({ ok: false, reason: "画布为空" });
  const convRef = useRef<ConvertResult>(conv);
  const hasContent = conv.ok || conv.reason !== "画布为空";

  const insertAsImage = async () => {
    const api = apiRef.current;
    if (!api || !hasContent || exporting) return;
    setExporting(true);
    try {
      const blob = await exportToBlob({
        elements: api.getSceneElements(),
        appState: {
          ...api.getAppState(),
          exportWithDarkMode: true, // 与暗色 UI 一致
          exportBackground: true,
        },
        files: api.getFiles(),
        mimeType: "image/png",
      });
      const dataUrl = await new Promise<string>((resolve, reject) => {
        const r = new FileReader();
        r.onload = () => resolve(r.result as string);
        r.onerror = () => reject(r.error);
        r.readAsDataURL(blob);
      });
      onInsert(dataUrl);
      onClose();
    } finally {
      setExporting(false);
    }
  };

  return (
    <div className="canvas-overlay">
      <div className="canvas-header">
        <span className="canvas-title">画板</span>
        <span className="canvas-hint">{importNote ?? "绘制完成后导出为图片或 Mermaid 代码插入输入框"}</span>
        <div className="canvas-actions">
          <button className="dialog-btn secondary" onClick={onClose}>
            <X size={14} /> 取消
          </button>
          <button
            className="dialog-btn secondary"
            disabled={!conv.ok}
            title={
              conv.ok
                ? "转换为 Mermaid 代码块，插入对话输入框（纯文本，省 Token，全模型可用）"
                : conv.reason
            }
            onClick={() => {
              const latest = convRef.current; // 点击取最新转换结果（state 有渲染延迟）
              if (!latest.ok) return;
              onInsertMermaid(latest.mermaid);
              onClose();
            }}
          >
            <Workflow size={14} /> 插入 Mermaid 到输入框
          </button>
          <button
            className="dialog-btn primary"
            disabled={!hasContent || exporting || !canInsertImage}
            title={
              canInsertImage
                ? "导出为 PNG 图片，插入对话输入框"
                : "当前模型未声明图片能力（设置 → 模型 → 能力 → 图片）"
            }
            onClick={() => void insertAsImage()}
          >
            <ImageDown size={14} /> {exporting ? "导出中…" : "插入图片到输入框"}
          </button>
        </div>
      </div>
      <div className="canvas-body">
        <Excalidraw
          excalidrawAPI={(api) => (apiRef.current = api)}
          theme="dark"
          initialData={loadLibrary}
          onLibraryChange={onLibraryChange}
          onChange={(els) => {
            const next = sceneToMermaid(els.filter((e) => !e.isDeleted));
            convRef.current = next;
            // 仅结果实质变化才驱动重渲染（详见 conv state 注释）
            setConv((prev) => {
              if (prev.ok !== next.ok) return next;
              if (!prev.ok && !next.ok && prev.reason !== next.reason) return next;
              return prev;
            });
          }}
        />
      </div>
    </div>
  );
}
