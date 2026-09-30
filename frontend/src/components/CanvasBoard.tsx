import { useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { Excalidraw, MainMenu, exportToBlob } from "@excalidraw/excalidraw";
import type { ExcalidrawImperativeAPI, ExcalidrawInitialDataState, LibraryItems } from "@excalidraw/excalidraw/types";
import "@excalidraw/excalidraw/index.css";
import { ImageDown, Workflow, Save, FileDown } from "lucide-react";
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
  draftSid,
  onInsert,
  onInsertMermaid,
}: {
  /** 当前模型是否声明图片能力（false 时导出按钮禁用并提示） */
  canInsertImage: boolean;
  /** 草稿归属会话 ID：非空时启用草稿持久化（关窗重开恢复内容） */
  draftSid?: string;
  onInsert: (dataUrl: string) => void;
  onInsertMermaid: (mermaid: string) => void;
}) {
  const { t, i18n } = useTranslation();
  // Excalidraw 内置语言包：zh→zh-CN，en→en；切换语言即整体换肤文案
  const excalidrawLang = i18n.language.startsWith("zh") ? "zh-CN" : "en";
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
  // 画板草稿持久化（draftSid 存在时启用）：按会话存到会话目录的
  // canvas_draft.json，关窗重开可增量续编。onChange 800ms 防抖写盘，
  // pagehide 兜底冲掉最后一次未落盘的编辑。
  const loadLibrary = async (): Promise<ExcalidrawInitialDataState | null> => {
    const json = await agentApi.getCanvasLibrary().catch(() => "");
    let libraryItems: LibraryItems | undefined;
    if (json) {
      try {
        libraryItems = JSON.parse(json) as LibraryItems;
      } catch {
        libraryItems = undefined; // 磁盘素材损坏时按空库工作（不影响画板主流程）
      }
    }
    if (!draftSid) return libraryItems ? { libraryItems } : null;
    const draftJson = await agentApi.getCanvasDraft(draftSid).catch(() => "");
    if (!draftJson) return libraryItems ? { libraryItems } : null;
    try {
      const draft = JSON.parse(draftJson) as {
        elements?: unknown[];
        files?: unknown;
        appState?: {
          scrollX?: number;
          scrollY?: number;
          zoom?: { value: number };
          defaultSidebarDockedPreference?: boolean;
        };
      };
      return {
        libraryItems,
        elements: (draft.elements ?? []) as ExcalidrawInitialDataState["elements"],
        files: draft.files as ExcalidrawInitialDataState["files"],
        appState: draft.appState as ExcalidrawInitialDataState["appState"],
      };
    } catch {
      return libraryItems ? { libraryItems } : null; // 草稿损坏按空白画布工作
    }
  };

  const draftTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const latestDraftRef = useRef<string>("");
  const saveDraft = (elements: unknown, appState: unknown, files: unknown) => {
    if (!draftSid) return;
    const st = appState as {
      scrollX?: number;
      scrollY?: number;
      zoom?: { value: number };
      defaultSidebarDockedPreference?: boolean;
    };
    latestDraftRef.current = JSON.stringify({
      elements,
      files,
      // 视口状态 + 素材库停靠（Pin）偏好——关窗重开保持停靠
      appState: {
        scrollX: st.scrollX,
        scrollY: st.scrollY,
        zoom: st.zoom,
        defaultSidebarDockedPreference: st.defaultSidebarDockedPreference,
      },
    });
    if (draftTimerRef.current) clearTimeout(draftTimerRef.current);
    draftTimerRef.current = setTimeout(() => {
      void agentApi.setCanvasDraft(draftSid, latestDraftRef.current).catch(() => {});
    }, 800);
  };
  // 显式保存（按钮 / Ctrl+S）：立即冲掉防抖落盘并给反馈
  const saveNow = () => {
    if (!draftSid) return;
    if (draftTimerRef.current) {
      clearTimeout(draftTimerRef.current);
      draftTimerRef.current = null;
    }
    if (!latestDraftRef.current) {
      toast(t("canvas.emptyNote"));
      return;
    }
    void agentApi
      .setCanvasDraft(draftSid, latestDraftRef.current)
      .then(() => toast(t("canvas.saved")))
      .catch((err) =>
        toast(t("canvas.saveFailed", { msg: err instanceof Error ? err.message : String(err) })),
      );
  };
  // 另存为（Ctrl+Shift+S）：导出 .excalidraw 场景文件（Excalidraw
  // 原生格式，可分享/备份/再导入），走系统保存对话框
  const saveAs = () => {
    const api = apiRef.current;
    if (!api) return;
    const scene = JSON.stringify({
      type: "excalidraw",
      version: 2,
      elements: api.getSceneElements(),
      appState: api.getAppState(),
      files: api.getFiles(),
    });
    void agentApi
      .exportCanvasScene(scene)
      .then((path) => {
        if (!path) return; // 用户取消
        toast(t("canvas.savedAs", { path }));
      })
      .catch((err) =>
        toast(t("canvas.saveAsFailed", { msg: err instanceof Error ? err.message : String(err) })),
      );
  };

  // 快捷键（桌面惯例）：Ctrl+S 静默保存草稿到会话目录；
  // Ctrl+Shift+S 另存为对话框导出 .excalidraw 文件。
  // 捕获阶段拦截——Excalidraw 原生也监听 Ctrl+S（弹保存对话框），
  // 需要抢在它前面并阻断传播。
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (!(e.ctrlKey || e.metaKey) || e.key.toLowerCase() !== "s") return;
      e.preventDefault();
      e.stopPropagation();
      if (e.shiftKey) saveAs();
      else saveNow();
    };
    window.addEventListener("keydown", onKey, true);
    return () => window.removeEventListener("keydown", onKey, true);
  });

  // 关窗兜底：防抖窗口期内最后一次编辑可能未落盘
  useEffect(() => {
    if (!draftSid) return;
    const flush = () => {
      if (latestDraftRef.current) {
        void agentApi.setCanvasDraft(draftSid, latestDraftRef.current).catch(() => {});
      }
    };
    window.addEventListener("pagehide", flush);
    return () => {
      window.removeEventListener("pagehide", flush);
      if (draftTimerRef.current) clearTimeout(draftTimerRef.current);
    };
  }, [draftSid]);
  const onLibraryChange = (items: unknown) => {
    if (saveTimerRef.current) clearTimeout(saveTimerRef.current);
    saveTimerRef.current = setTimeout(() => {
      void agentApi.setCanvasLibrary(JSON.stringify(items));
    }, 800);
  };

  // 瞬态提示统一走 Excalidraw 内置 toast（头部状态栏已移除）
  const toast = (message: string) => apiRef.current?.setToast({ message });

  // 画板主题跟随应用主题（窗口内 CustomEvent，由 theme.ts 广播）
  const [appTheme, setAppTheme] = useState<"dark" | "light">(() =>
    document.documentElement.dataset.theme === "light" ? "light" : "dark",
  );
  useEffect(() => {
    const onTheme = (e: Event) =>
      setAppTheme((e as CustomEvent).detail === "light" ? "light" : "dark");
    window.addEventListener("tars:theme-change", onTheme);
    return () => window.removeEventListener("tars:theme-change", onTheme);
  }, []);

  // 素材库导入（#addLibrary hash 兜底路径使用；主链路是应用内素材
  // 浏览窗口 + 启动早期导入门 LibraryImportGate，不经由画板）。
  const importFrom = (sourceUrl: string) => {
    toast(t("canvas.importing"));
    agentApi
      .importCanvasLibrary(sourceUrl)
      .then(({ json, added }) => {
        if (apiRef.current && json) {
          void apiRef.current.updateLibrary({ libraryItems: JSON.parse(json) });
        }
        toast(added > 0 ? t("canvas.imported", { count: added }) : t("canvas.importExists"));
      })
      .catch((err) =>
        toast(t("canvas.importFailed", { msg: err instanceof Error ? err.message : String(err) })),
      );
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
  const [conv, setConv] = useState<ConvertResult>({ ok: false, reason: "canvas.reason.empty" });
  const convRef = useRef<ConvertResult>(conv);
  const hasContent = conv.ok || conv.reason !== "canvas.reason.empty";

  const insertAsImage = async () => {
    const api = apiRef.current;
    if (!api || !hasContent || exporting) return;
    setExporting(true);
    try {
      const blob = await exportToBlob({
        elements: api.getSceneElements(),
        appState: {
          ...api.getAppState(),
          exportWithDarkMode: appTheme === "dark", // 跟随应用主题
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
      onInsert(dataUrl); // 关窗由调用方（CanvasWindow 的回调）负责
    } finally {
      setExporting(false);
    }
  };

  return (
    <div className="canvas-overlay">
      {/* 头部状态栏已移除（标题由副窗口 Tab 承担）；保存/导出动作
          收进 Excalidraw 主菜单（汉堡），瞬态提示走内置 toast */}
      <div className="canvas-body">
        <Excalidraw
          excalidrawAPI={(api) => (apiRef.current = api)}
          theme={appTheme}
          langCode={excalidrawLang}
          // 素材库侧栏的 Pin（停靠）按钮仅在宽度 ≥ dockedSidebarBreakpoint
          // 时渲染；默认断点高于副窗口常用宽度，降到 620 让 Pin 常驻可用。
          // 停靠偏好存在 appState，随草稿持久化（关窗重开仍停靠）。
          UIOptions={{ dockedSidebarBreakpoint: 620 }}
          initialData={loadLibrary}
          onLibraryChange={onLibraryChange}
          onChange={(els, appState, files) => {
            saveDraft(els, appState, files);
            const next = sceneToMermaid(els.filter((e) => !e.isDeleted));
            convRef.current = next;
            // 仅结果实质变化才驱动重渲染（详见 conv state 注释）
            setConv((prev) => {
              if (prev.ok !== next.ok) return next;
              if (!prev.ok && !next.ok && prev.reason !== next.reason) return next;
              return prev;
            });
          }}
        >
          <MainMenu>
            {draftSid && (
              <MainMenu.Item icon={<Save size={16} />} onSelect={saveNow}>
                {t("canvas.save")}
              </MainMenu.Item>
            )}
            <MainMenu.Item icon={<FileDown size={16} />} onSelect={saveAs}>
              {t("canvas.saveAs")}
            </MainMenu.Item>
            <MainMenu.Item
              icon={<Workflow size={16} />}
              disabled={!conv.ok}
              onSelect={() => {
                const latest = convRef.current; // 点击取最新转换结果（state 有渲染延迟）
                if (!latest.ok) return;
                onInsertMermaid(latest.mermaid); // 关窗由调用方负责
              }}
            >
              {t("canvas.exportMermaid")}
            </MainMenu.Item>
            <MainMenu.Item
              icon={<ImageDown size={16} />}
              disabled={!hasContent || exporting || !canInsertImage}
              onSelect={() => void insertAsImage()}
            >
              {exporting ? t("canvas.exporting") : t("canvas.exportImage")}
            </MainMenu.Item>
            <MainMenu.Separator />
            <MainMenu.DefaultItems.SearchMenu />
            <MainMenu.DefaultItems.Help />
            <MainMenu.DefaultItems.ClearCanvas />
            <MainMenu.Separator />
            <MainMenu.DefaultItems.ToggleTheme />
            <MainMenu.DefaultItems.ChangeCanvasBackground />
          </MainMenu>
        </Excalidraw>
      </div>
    </div>
  );
}
