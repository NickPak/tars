import { useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { Events } from "@wailsio/runtime";
import { EditorContent, useEditor, useEditorState } from "@tiptap/react";
import StarterKit from "@tiptap/starter-kit";
import { Markdown } from "@tiptap/markdown";
import { Mathematics } from "@tiptap/extension-mathematics";
import { Image } from "@tiptap/extension-image";
import { Placeholder } from "@tiptap/extensions";
import FileHandler from "@tiptap/extension-file-handler";
import {
  ArrowUp,
  Code,
  Heading1,
  Heading2,
  ImagePlus,
  List,
  ListOrdered,
  Palette,
  Radical,
  Sigma,
  Square,
  SquareCode,
  TextQuote,
} from "lucide-react";
import { useChatStore } from "../store/chatStore";
import { useLayoutStore } from "../store/layoutStore";
import ImagePreview from "./ImagePreview";
import { agentApi } from "../services/agentApi";

/**
 * 图片节点（内联文档）：markdown 序列化时输出占位符 [图片] 而非
 * base64——真实图片数据在发送时经文档遍历收集进 images[] 上行，
 * 占位符顺序与 images[] 下标一一对应（LLM 可据此定位图片在文中的位置）。
 * 默认的 ![](data:...) 序列化会把 base64 灌进文本，必须覆盖。
 */
const ComposerImage = Image.extend({
  renderMarkdown: () => "[图片]",
});

/** 工具栏按钮：active 高亮当前光标所在的格式状态 */
function ToolbarBtn({
  title,
  active,
  onClick,
  children,
}: {
  title: string;
  active?: boolean;
  onClick: () => void;
  children: React.ReactNode;
}) {
  return (
    <button
      type="button"
      className={`composer-toolbar-btn${active ? " on" : ""}`}
      title={title}
      aria-label={title}
      onClick={onClick}
    >
      {children}
    </button>
  );
}

/**
 * 聊天输入框（TipTap 富文本）：
 * - 输入即 Markdown：发送时 editor.getMarkdown() 取回 md 文本上行，
 *   后端契约（text + images[]）不变；
 * - 图片是文档内联节点：光标可自由移到图片前后编辑；按钮/粘贴/拖放
 *   统一在光标处插入；入口受模型 supportsImages 能力门控；
 * - 非受控模式：文档状态由编辑器持有，发送时现取（避免受控写入
 *   导致的光标跳动）。
 */
export default function ChatInput() {
  const { t } = useTranslation();
  /** 图片双击预览（lightbox） */
  const [preview, setPreview] = useState<string | null>(null);
  /** 编辑器高度（顶部把手拖拽后固定为该值；null = 随内容自适应）。
   *  存 layoutStore：消息列表订阅它做底部滚动锚定。 */
  const editorH = useLayoutStore((s) => s.composerHeight);
  const setEditorH = useLayoutStore((s) => s.setComposerHeight);
  const editorWrapRef = useRef<HTMLDivElement>(null);


  const isStreaming = useChatStore((s) => s.isStreaming);
  const activeId = useChatStore((s) => s.activeId);
  const send = useChatStore((s) => s.send);
  const cancel = useChatStore((s) => s.cancel);
  const supportsImages = useChatStore((s) => s.model?.supportsImages ?? false);
  const fileRef = useRef<HTMLInputElement>(null);

  // ref 桥：FileHandler/handleKeyDown 回调在编辑器创建时闭包，
  // 经 ref 读取最新的流式状态/能力门控/预览回调。
  const stateRef = useRef({ isStreaming, supportsImages });
  stateRef.current = { isStreaming, supportsImages };
  const doSendRef = useRef<() => void>(() => {});
  const previewRef = useRef(setPreview);
  previewRef.current = setPreview;

  // 读取图片文件并在光标处插入图片节点（编辑器由调用方传入：
  // 粘贴/拖放用回调参数里的，按钮用 useEditor 实例）
  const insertImageFiles = (ed: NonNullable<ReturnType<typeof useEditor>>, files: File[] | FileList | null) => {
    if (!files) return;
    for (const f of Array.from(files)) {
      if (!f.type.startsWith("image/")) continue;
      const reader = new FileReader();
      reader.onload = () => {
        if (typeof reader.result === "string") {
          ed.chain().focus().setImage({ src: reader.result }).run();
        }
      };
      reader.readAsDataURL(f);
    }
  };

  const editor = useEditor({
    extensions: [
      StarterKit.configure({
        link: {
          // 引用链接（tarsref:path:Lx-Ly）自定义 scheme 需显式放行；
          // 点击行为由 handleDOMEvents 拦截（跳编辑器而非浏览器）
          protocols: ["tarsref"],
          openOnClick: false,
          autolink: false,
        },
      }),
      Markdown,
      Mathematics, // katex 样式已由 Markdown.tsx 全局引入；$...$/$$...$$ 与 markdown 双向序列化
      ComposerImage,
      // placeholder 在建编辑器时取值；切换语言后下次挂载生效（可接受）
      Placeholder.configure({ placeholder: t("chat.placeholder") }),
      FileHandler.configure({
        // 注意：allowedMimeTypes 是精确匹配（不支持 "image/*" 通配符），
        // 传了反而全被过滤掉——类型过滤由 insertImageFiles 的前缀判断负责。
        consumePasteEvent: true,
        onPaste: (e, files) => {
          if (stateRef.current.supportsImages) insertImageFiles(e, files);
        },
        onDrop: (e, files) => {
          if (stateRef.current.supportsImages) insertImageFiles(e, files);
        },
      }),
    ],
    editorProps: {
      handleKeyDown: (_view, event) => {
        // Enter 发送 / Shift+Enter 换行；isComposing 保护中文输入法选词
        if (event.key === "Enter" && !event.shiftKey && !event.isComposing) {
          event.preventDefault();
          doSendRef.current();
          return true;
        }
        return false;
      },
      handleDOMEvents: {
        // 点击引用链接 → 打开对应文件并定位/高亮行范围
        click: (_view, event) => {
          const t = (event.target as HTMLElement).closest?.("a.composer-ref");
          if (!t) return false;
          event.preventDefault();
          const href = t.getAttribute("href") ?? "";
          const payload = decodeURIComponent(href.slice("tarsref:".length));
          const m = /^(.*):L(\d+)(?:-(\d+))?$/.exec(payload);
          if (!m) return true;
          const sid = useChatStore.getState().activeId;
          if (sid) {
            void agentApi.revealEditorRange(sid, m[1], Number(m[2]), Number(m[3] ?? m[2]));
          }
          return true;
        },
        // 双击图片节点 → 预览（target 即 <img>，无需坐标换算）
        dblclick: (_view, event) => {
          const t = event.target as HTMLElement;
          if (t.tagName === "IMG") {
            const src = t.getAttribute("src");
            if (src) previewRef.current(src);
            return true;
          }
          return false;
        },
      },
    },
  });

  // 跨组件插入通道（文件查看器的"引用到输入框"等）：消费后即清空
  const composerInsert = useLayoutStore((s) => s.composerInsert);
  const setComposerInsert = useLayoutStore((s) => s.setComposerInsert);
  useEffect(() => {
    if (!editor || !composerInsert) return;
    editor.chain().focus().insertContent(composerInsert).run();
    setComposerInsert(null);
  }, [editor, composerInsert, setComposerInsert]);

  // 画板独立窗口的产出回流：后端把 canvas:insert-image /
  // canvas:insert-mermaid 广播到本窗口，插入光标处
  useEffect(() => {
    if (!editor) return;
    const offImg = Events.On("canvas:insert-image", (ev) => {
      editor.chain().focus().setImage({ src: ev.data as string }).run();
    });
    const offMmd = Events.On("canvas:insert-mermaid", (ev) => {
      // 以代码块插入；markdown 扩展会解析为 code block 节点
      editor.chain().focus().insertContent(`\n\`\`\`mermaid\n${ev.data}\n\`\`\`\n`).run();
    });
    // 编辑器独立窗口的框选引用回流（path:Lx-Ly）：以链接形态插入，
    // 点击可回跳编辑器定位高亮；发送时序列化后还原为纯文本引用。
    const offRef = Events.On("editor:insert-reference", (ev) => {
      const ref = String(ev.data);
      editor
        .chain()
        .focus()
        .insertContent(`<a href="tarsref:${encodeURIComponent(ref)}" class="composer-ref">${ref}</a>`)
        .run();
    });
    return () => {
      offImg();
      offMmd();
      offRef();
    };
  }, [editor]);

  // 发送按钮禁用态与工具栏激活态订阅（非受控模式下经 useEditorState 取）
  const editorState = useEditorState({
    editor,
    selector: (c) => {
      const e = c.editor;
      if (!e) return { isEmpty: true, h1: false, h2: false, bullet: false, ordered: false, quote: false, code: false, codeBlock: false };
      return {
        isEmpty: e.isEmpty,
        h1: e.isActive("heading", { level: 1 }),
        h2: e.isActive("heading", { level: 2 }),
        bullet: e.isActive("bulletList"),
        ordered: e.isActive("orderedList"),
        quote: e.isActive("blockquote"),
        code: e.isActive("code"),
        codeBlock: e.isActive("codeBlock"),
      };
    }, // equalityFn 缺省即 fast-deep-equal，对象选择器安全
  });

  doSendRef.current = () => {
    if (!editor) return;
    // 文档序遍历收集图片（与文本中 [图片] 占位符的出现顺序一致）
    const imgs: string[] = [];
    editor.state.doc.descendants((node) => {
      if (node.type.name === "image" && node.attrs.src) imgs.push(node.attrs.src);
    });
    let text = editor.getMarkdown().trim();
    // 引用链接还原为纯文本引用（path:Lx-Ly）——后端按此格式展开为
    // 真实代码片段，tarsref: 协议只是输入框内的展示/交互外壳。
    text = text.replace(/\[([^\]]+)\]\(tarsref:[^)]*\)/g, "$1");
    // 过滤剪贴板残留的 IDE 图片引用文本（如从 CodeBuddy 复制时带入的
    // "@image:C:\..."——该引用只在原 IDE 内有意义，属于粘贴垃圾）。
    text = text.replace(/@image:\S+/g, "").trim();
    // 多图时给占位符编号（[图片1]/[图片2]…），LLM 引用不歧义
    if (imgs.length > 1) {
      let n = 0;
      text = text.replace(/\[图片\]/g, () => `[图片${++n}]`);
    }
    const { isStreaming: streaming } = stateRef.current;
    if ((!text && imgs.length === 0) || streaming) return;
    editor.commands.clearContent();
    void send(text, imgs);
  };

  // 顶部把手拖拽调整输入区高度：上拖加高，范围 [80px, 60vh]。
  // 首次拖拽以当前实际高度为基准（此前随内容自适应），拖过即固定。
  const onGripDown = (e: React.MouseEvent) => {
    e.preventDefault();
    const startY = e.clientY;
    const startH = editorH ?? editorWrapRef.current?.getBoundingClientRect().height ?? 60;
    const onMove = (ev: MouseEvent) => {
      setEditorH(
        Math.min(window.innerHeight * 0.6, Math.max(80, startH + (startY - ev.clientY))),
      );
    };
    const onUp = () => {
      document.removeEventListener("mousemove", onMove);
      document.removeEventListener("mouseup", onUp);
    };
    document.addEventListener("mousemove", onMove);
    document.addEventListener("mouseup", onUp);
  };

  return (
    <div className="composer">
      <div
        className="composer-grip"
        onMouseDown={onGripDown}
        role="separator"
        aria-orientation="horizontal"
        title={t("chat.resizeGrip")}
      >
        <span />
      </div>
      <div className="composer-box">
        <div className="composer-main">
          {editor && (
            <div className="composer-toolbar">
              <ToolbarBtn title={t("chat.h1")} active={editorState?.h1} onClick={() => editor.chain().focus().toggleHeading({ level: 1 }).run()}><Heading1 size={14} /></ToolbarBtn>
              <ToolbarBtn title={t("chat.h2")} active={editorState?.h2} onClick={() => editor.chain().focus().toggleHeading({ level: 2 }).run()}><Heading2 size={14} /></ToolbarBtn>
              <span className="composer-toolbar-sep" />
              <ToolbarBtn title={t("chat.bullet")} active={editorState?.bullet} onClick={() => editor.chain().focus().toggleBulletList().run()}><List size={14} /></ToolbarBtn>
              <ToolbarBtn title={t("chat.ordered")} active={editorState?.ordered} onClick={() => editor.chain().focus().toggleOrderedList().run()}><ListOrdered size={14} /></ToolbarBtn>
              <ToolbarBtn title={t("chat.quote")} active={editorState?.quote} onClick={() => editor.chain().focus().toggleBlockquote().run()}><TextQuote size={14} /></ToolbarBtn>
              <span className="composer-toolbar-sep" />
              <ToolbarBtn title={t("chat.inlineCode")} active={editorState?.code} onClick={() => editor.chain().focus().toggleCode().run()}><Code size={14} /></ToolbarBtn>
              <ToolbarBtn title={t("chat.codeBlock")} active={editorState?.codeBlock} onClick={() => editor.chain().focus().toggleCodeBlock().run()}><SquareCode size={14} /></ToolbarBtn>
              <span className="composer-toolbar-sep" />
              <ToolbarBtn title={t("chat.inlineMath")} onClick={() => editor.chain().focus().insertInlineMath({ latex: "" }).run()}><Sigma size={14} /></ToolbarBtn>
              <ToolbarBtn title={t("chat.blockMath")} onClick={() => editor.chain().focus().insertBlockMath({ latex: "" }).run()}><Radical size={14} /></ToolbarBtn>
              <span className="composer-toolbar-sep" />
              <ToolbarBtn title={t("chat.canvasTip")} onClick={() => void agentApi.openAuxTab(activeId ?? "", "canvas", "")}><Palette size={14} /></ToolbarBtn>
              {supportsImages && (
                <>
                  <span className="composer-toolbar-sep" />
                  <ToolbarBtn title={t("chat.addImage")} onClick={() => fileRef.current?.click()}><ImagePlus size={14} /></ToolbarBtn>
                </>
              )}
            </div>
          )}
          <div
            ref={editorWrapRef}
            className="composer-editor-wrap"
            style={editorH != null ? { height: editorH } : undefined}
            onClick={() => editor?.commands.focus()}
          >
            <EditorContent editor={editor} className="composer-editor" />
          </div>
        </div>
        {supportsImages && (
          <input
            ref={fileRef}
            type="file"
            accept="image/*"
            multiple
            hidden
            onChange={(e) => {
              if (editor) insertImageFiles(editor, e.target.files);
              e.target.value = ""; // 允许重复选择同一文件
            }}
          />
        )}
        {isStreaming ? (
          <button
            className="composer-btn stop"
            onClick={() => void cancel()}
            aria-label={t("chat.stop")}
            title={t("chat.stop")}
          >
            <Square size={18} fill="currentColor" />
          </button>
        ) : (
          <button
            className="composer-btn send"
            onClick={() => doSendRef.current()}
            disabled={editorState?.isEmpty ?? true}
            aria-label={t("chat.send")}
            title={t("chat.send")}
          >
            <ArrowUp size={20} />
          </button>
        )}
      </div>
      <div className="composer-hint">{t("chat.hint")}</div>
      <ImagePreview src={preview} onClose={() => setPreview(null)} />
    </div>
  );
}
