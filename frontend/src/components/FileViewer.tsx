import { lazy, Suspense, useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { MessageSquarePlus } from "lucide-react";
import { Events } from "@wailsio/runtime";
import { agentApi } from "../services/agentApi";
import type { Editor } from "./CodeEditor";

const CodeEditor = lazy(() => import("./CodeEditor"));

/**
 * 工作区文件编辑器（Monaco）：独立 OS 窗口（FileService.OpenEditorWindow
 * 加载 ?view=editor&sid=..&path=..，main.tsx 分流渲染本组件），自带
 * 系统标题栏，与主窗口并排工作——边对话边浏览/编辑文件。
 *
 * - 可编辑：Ctrl+S 保存（写回工作区，模型下一轮即可读到改动）
 * - 框选代码 → "引用到输入框"：经 composerInsert 通道……注意：
 *   独立窗口内 layoutStore 是窗口本地状态，引用需经后端事件回流主窗口。
 */
export default function FileViewer({
  sessionId,
  path,
  tabId,
  onClose,
}: {
  sessionId: string;
  path: string;
  /** Tab ID：接收 Tab 右键菜单转发的动作（保存/关闭） */
  tabId?: string;
  onClose: () => void;
}) {
  const { t } = useTranslation();
  const [content, setContent] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [editor, setEditor] = useState<Editor | null>(null);
  const [dirty, setDirty] = useState(false);
  const [saving, setSaving] = useState(false);
  const [note, setNote] = useState<string | null>(null);
  // 拖选松开后在选区末尾弹出的 "Add To Chat" 浮动按钮（fixed 定位，
  // 不参与布局——选区交互期间禁止任何布局抖动，详见下方指针捕获注释）
  const [selPopup, setSelPopup] = useState<{ x: number; y: number } | null>(null);
  const editorRef = useRef<Editor | null>(null);
  editorRef.current = editor;
  // 状态栏数据：光标位置拖拽期间延迟 flush（同指针捕获红线）；
  // tabSize/语言挂载后取一次；换行符/编码由内容决定（读写均 UTF-8）
  const [cursor, setCursor] = useState({ line: 1, col: 1 });
  const pendingCursorRef = useRef<{ line: number; col: number } | null>(null);
  const [meta, setMeta] = useState({ tabSize: 4, lang: "" });
  const eol = content?.includes("\r\n") ? "CRLF" : "LF";


  useEffect(() => {
    setContent(null);
    setError(null);
    setDirty(false);
    agentApi
      .readWorkspaceFile(sessionId, path)
      .then(setContent)
      .catch((e) => setError(e instanceof Error ? e.message : String(e)));
  }, [sessionId, path]);

  // 指针拖拽跟踪。
  // 关键：拖拽选区期间绝不能触发 React 重渲染——重渲染引起的布局抖动
  // 会让旧版 WebView2 丢掉 Monaco 的指针捕获（lostpointercapture），
  // 选区跟踪永久冻结（表现为"只能选 2 行"）。悬停弹窗逻辑以此守卫。
  const draggingRef = useRef(false);
  // 在选区末行右下角弹 "Add To Chat" 浮动按钮（fixed 定位不参与布局）。
  // 仅在没有激活指针拖拽时调用——选区交互期间禁止重渲染/布局抖动。
  // 坐标近似不变时复用旧值，避免悬停移动造成的重渲染风暴。
  const showPopupForSelection = () => {
    const ed = editorRef.current;
    const sel = ed?.getSelection();
    if (!ed || !sel || sel.isEmpty()) {
      setSelPopup(null);
      return;
    }
    const vis = ed.getScrolledVisiblePosition(sel.getEndPosition());
    const dom = ed.getDomNode();
    if (!vis || !dom) return;
    const r = dom.getBoundingClientRect();
    const x = Math.min(r.left + vis.left + 10, window.innerWidth - 150);
    const y = Math.min(r.top + vis.top + vis.height + 6, window.innerHeight - 50);
    setSelPopup((prev) =>
      prev && Math.abs(prev.x - x) < 4 && Math.abs(prev.y - y) < 4 ? prev : { x, y },
    );
  };
  const showPopupRef = useRef(showPopupForSelection);
  showPopupRef.current = showPopupForSelection;
  const hideTimerRef = useRef<number>(0);
  const scheduleHidePopup = () => {
    window.clearTimeout(hideTimerRef.current);
    hideTimerRef.current = window.setTimeout(() => setSelPopup(null), 250);
  };
  useEffect(() => {
    const down = (e: PointerEvent) => {
      if (e.button === 0) draggingRef.current = true; // 仅左键参与选区跟踪
      // 任何按键按下都先关掉弹窗（右键的 Monaco 菜单不能与弹窗共存）；
      // 点在弹窗自身上除外（否则按钮在 click 前就被卸载了）
      if (!(e.target as HTMLElement).closest?.(".editor-add-chat")) {
        setSelPopup(null);
      }
    };
    const up = (e: PointerEvent) => {
      if (e.button !== 0) return;
      draggingRef.current = false;
      if (pendingCursorRef.current !== null) {
        setCursor(pendingCursorRef.current); // 拖拽期间积压的光标位置一次性 flush
        pendingCursorRef.current = null;
      }
      // 弹窗显隐由悬停驱动（onMouseMove），此处无需处理
    };
    window.addEventListener("pointerdown", down, true);
    window.addEventListener("pointerup", up, true);
    return () => {
      window.removeEventListener("pointerdown", down, true);
      window.removeEventListener("pointerup", up, true);
    };
  }, []);

  // 悬停驱动弹窗：鼠标进入选区/高亮区 → 显示；离开 → 延迟隐藏
  // （250ms 窗口期允许鼠标移到按钮上；按钮自身的悬停会取消隐藏）。
  // 拖拽选区进行中直接返回——指针捕获期间禁止任何重渲染。
  useEffect(() => {
    if (!editor) return;
    const sub = editor.onMouseMove((e) => {
      if (draggingRef.current) return;
      const pos = e.target.position;
      const sel = editor.getSelection();
      const inSel =
        pos !== null &&
        sel !== null &&
        !sel.isEmpty() &&
        (pos.lineNumber > sel.startLineNumber ||
          (pos.lineNumber === sel.startLineNumber && pos.column >= sel.startColumn)) &&
        (pos.lineNumber < sel.endLineNumber ||
          (pos.lineNumber === sel.endLineNumber && pos.column <= sel.endColumn));
      if (inSel) {
        window.clearTimeout(hideTimerRef.current);
        showPopupRef.current();
      } else {
        scheduleHidePopup();
      }
    });
    return () => {
      sub.dispose();
      window.clearTimeout(hideTimerRef.current);
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [editor]);

  const flash = (text: string, ms = 2000) => {
    setNote(text);
    setTimeout(() => setNote(null), ms);
  };

  const save = () => {
    if (!editor || !dirty || saving) return;
    setSaving(true);
    agentApi
      .writeWorkspaceFile(sessionId, path, editor.getValue())
      .then(() => {
        setDirty(false);
        flash(t("editor.saved"));
      })
      .catch((e) =>
        flash(t("editor.saveFailed", { msg: e instanceof Error ? e.message : String(e) }), 4000),
      )
      .finally(() => setSaving(false));
  };

  // Ctrl+S 保存（本栏聚焦时）
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if ((e.ctrlKey || e.metaKey) && !e.shiftKey && e.key.toLowerCase() === "s") {
        e.preventDefault();
        save();
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  });

  // Tab 右键菜单动作（保存/关闭经窗口内 CustomEvent 转发至此——
  // 编辑器实例与脏状态都在本组件，菜单只发意图）
  const saveRef = useRef(save);
  saveRef.current = save;
  const dirtyRef = useRef(dirty);
  dirtyRef.current = dirty;
  useEffect(() => {
    if (!tabId) return;
    const onAction = (e: Event) => {
      const { id, action } = (e as CustomEvent).detail ?? {};
      if (id !== tabId) return;
      if (action === "save") saveRef.current();
      if (action === "close") {
        if (dirtyRef.current && !window.confirm(t("editor.confirmCloseDirty"))) return;
        onClose();
      }
    };
    window.addEventListener("aux:tab-action", onAction);
    return () => window.removeEventListener("aux:tab-action", onAction);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [tabId]);

  const insertReference = () => {
    const ed = editorRef.current;
    if (!ed) return;
    const sel = ed.getSelection();
    if (!sel || sel.isEmpty()) return;
    const ref =
      sel.startLineNumber === sel.endLineNumber
        ? `${path}:L${sel.startLineNumber}`
        : `${path}:L${sel.startLineNumber}-${sel.endLineNumber}`;
    // 独立窗口的 store 是窗口本地状态——引用经后端事件回流主窗口输入框
    void agentApi.editorInsertReference(sessionId, ref).then(() => flash(t("editor.referenced", { ref })));
    setSelPopup(null);
  };

  // Monaco 右键菜单注册 "Add To Chat"
  useEffect(() => {
    if (!editor) return;
    const disposable = editor.addAction({
      id: "tars.addToChat",
      label: "Add To Chat",
      contextMenuGroupId: "9_cutcopypaste",
      run: () => insertReference(),
    });
    return () => disposable.dispose();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [editor]);

  // ---- 引用链接回流定位（主窗口点击 path:Lx-Ly 链接）----
  // 高亮 start~end 整行并定位到起始行；用户点击编辑器任意处后清除。
  const revealDecoRef = useRef<ReturnType<Editor["createDecorationsCollection"]> | null>(null);
  const applyReveal = (start: number, end: number) => {
    const ed = editorRef.current;
    if (!ed) return;
    const max = ed.getModel()?.getLineCount() ?? end;
    const s = Math.max(1, Math.min(start, max));
    const e = Math.max(s, Math.min(end, max));
    revealDecoRef.current?.clear();
    revealDecoRef.current = ed.createDecorationsCollection([
      {
        range: { startLineNumber: s, startColumn: 1, endLineNumber: e, endColumn: 1 },
        options: {
          isWholeLine: true,
          className: "editor-ref-lines",
          linesDecorationsClassName: "editor-ref-lines-gutter",
          stickiness: 1,
        },
      },
    ]);
    // 高亮范围同时设为选区：右键 "Add To Chat" 直接可用，浮动按钮
    // 随即出现（等一帧让 revealLine 的布局落定再取坐标）
    const endCol = ed.getModel()?.getLineMaxColumn(e) ?? 1;
    ed.setSelection({ startLineNumber: s, startColumn: 1, endLineNumber: e, endColumn: endCol });
    ed.revealLineInCenter(s);
    ed.focus();
    requestAnimationFrame(() => showPopupForSelection());
  };
  const applyRevealRef = useRef(applyReveal);
  applyRevealRef.current = applyReveal;

  // 事件通道（文件已打开时即时生效）
  useEffect(() => {
    const off = Events.On("aux:editor-reveal", (ev) => {
      const d = ev.data as { path: string; start: number; end: number };
      if (d.path === path) applyRevealRef.current(d.start, d.end);
    });
    return () => off();
  }, [path]);

  // 拉取兜底（Tab 刚打开、事件先于挂载到达时）
  useEffect(() => {
    if (!editor) return;
    void agentApi
      .takePendingReveal(path)
      .then((r) => {
        if (r.ok) applyRevealRef.current(r.start, r.end);
      })
      .catch(() => {});
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [editor, path]);

  // 状态栏：光标位置（拖拽中延迟 flush）与文件元信息（挂载取一次）
  useEffect(() => {
    if (!editor) return;
    const model = editor.getModel();
    const langId = model?.getLanguageId() ?? "";
    const LANG_NAMES: Record<string, string> = {
      cpp: "C++", csharp: "C#", plaintext: "Plain Text",
    };
    setMeta({
      tabSize: model?.getOptions().tabSize ?? 4,
      lang: LANG_NAMES[langId] ?? langId.charAt(0).toUpperCase() + langId.slice(1),
    });
    const sub = editor.onDidChangeCursorPosition((e) => {
      const v = { line: e.position.lineNumber, col: e.position.column };
      if (draggingRef.current) {
        pendingCursorRef.current = v; // 拖拽中只记账，避免重渲染打断指针捕获
      } else {
        setCursor(v);
      }
    });
    return () => sub.dispose();
  }, [editor]);

  // 用户左键点击编辑器后清除定位高亮（右键要留给 Monaco 上下文菜单——
  // 此时清装饰的布局抖动会把菜单挤掉）
  useEffect(() => {
    if (!editor) return;
    const sub = editor.onMouseDown((e) => {
      if (e.event.leftButton) revealDecoRef.current?.clear();
    });
    return () => sub.dispose();
  }, [editor]);

  return (
    <div className="editor-pane">
      <div className="editor-pane-header">
        <span className="editor-pane-path" title={path}>
          {path}
          {dirty && <span className="editor-pane-dirty">●</span>}
        </span>
        <span className="editor-pane-note">{note}</span>
        {/* 保存/外部打开/关闭在 Tab 右键菜单（aux:tab-action 转发）；
            引用入口为悬停选区时的浮动 "Add To Chat" */}

      </div>
      <div className="editor-pane-body">
        {error && <div className="code-editor-error">{error}</div>}
        {!error && content === null && <div className="settings-loading">{t("editor.loading")}</div>}
        {!error && content !== null && (
          <Suspense fallback={<div className="code-editor-loading">{t("editor.loadingEditor")}</div>}>
            <CodeEditor
              path={path}
              value={content}
              readOnly={false}
              onChange={() => setDirty(true)}
              onMount={(ed) => setEditor(ed)}
            />
          </Suspense>
        )}
      </div>
      {/* VS Code 风格状态栏（同时充当下边框缩放的友好热区） */}
      {!error && content !== null && (
        <div className="editor-statusbar">
          <span className="editor-statusbar-spacer" />
          <span>Ln {cursor.line}, Col {cursor.col}</span>
          <span>Tab Size: {meta.tabSize}</span>
          <span>UTF-8</span>
          <span>{eol}</span>
          <span>{meta.lang}</span>
        </div>
      )}
      {/* 悬停选区/高亮区时浮出的 "Add To Chat"（fixed 定位，不参与布局） */}
      {selPopup && (
        <button
          className="editor-add-chat"
          style={{ left: selPopup.x, top: selPopup.y }}
          onMouseDown={(e) => e.preventDefault()} // 不抢走编辑器焦点/选区
          onMouseEnter={() => window.clearTimeout(hideTimerRef.current)}
          onMouseLeave={scheduleHidePopup}
          onClick={insertReference}
        >
          <MessageSquarePlus size={13} />
          Add To Chat
        </button>
      )}
    </div>
  );
}
