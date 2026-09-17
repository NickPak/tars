import { useRef, useState } from "react";
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
  Radical,
  Sigma,
  Square,
  SquareCode,
  TextQuote,
} from "lucide-react";
import { useChatStore } from "../store/chatStore";
import { useLayoutStore } from "../store/layoutStore";
import ImagePreview from "./ImagePreview";

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
  /** 图片双击预览（lightbox） */
  const [preview, setPreview] = useState<string | null>(null);
  /** 编辑器高度（顶部把手拖拽后固定为该值；null = 随内容自适应）。
   *  存 layoutStore：消息列表订阅它做底部滚动锚定。 */
  const editorH = useLayoutStore((s) => s.composerHeight);
  const setEditorH = useLayoutStore((s) => s.setComposerHeight);
  const editorWrapRef = useRef<HTMLDivElement>(null);
  const isStreaming = useChatStore((s) => s.isStreaming);
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
      StarterKit,
      Markdown,
      Mathematics, // katex 样式已由 Markdown.tsx 全局引入；$...$/$$...$$ 与 markdown 双向序列化
      ComposerImage,
      Placeholder.configure({ placeholder: "向 TARS 提问…（支持 Markdown）" }),
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
        title="拖拽调整输入区高度"
      >
        <span />
      </div>
      <div className="composer-box">
        <div className="composer-main">
          {editor && (
            <div className="composer-toolbar">
              <ToolbarBtn title="一级标题" active={editorState?.h1} onClick={() => editor.chain().focus().toggleHeading({ level: 1 }).run()}><Heading1 size={14} /></ToolbarBtn>
              <ToolbarBtn title="二级标题" active={editorState?.h2} onClick={() => editor.chain().focus().toggleHeading({ level: 2 }).run()}><Heading2 size={14} /></ToolbarBtn>
              <span className="composer-toolbar-sep" />
              <ToolbarBtn title="无序列表" active={editorState?.bullet} onClick={() => editor.chain().focus().toggleBulletList().run()}><List size={14} /></ToolbarBtn>
              <ToolbarBtn title="有序列表" active={editorState?.ordered} onClick={() => editor.chain().focus().toggleOrderedList().run()}><ListOrdered size={14} /></ToolbarBtn>
              <ToolbarBtn title="引用块" active={editorState?.quote} onClick={() => editor.chain().focus().toggleBlockquote().run()}><TextQuote size={14} /></ToolbarBtn>
              <span className="composer-toolbar-sep" />
              <ToolbarBtn title="行内代码" active={editorState?.code} onClick={() => editor.chain().focus().toggleCode().run()}><Code size={14} /></ToolbarBtn>
              <ToolbarBtn title="代码块" active={editorState?.codeBlock} onClick={() => editor.chain().focus().toggleCodeBlock().run()}><SquareCode size={14} /></ToolbarBtn>
              <span className="composer-toolbar-sep" />
              <ToolbarBtn title="行内公式（$...$）" onClick={() => editor.chain().focus().insertInlineMath({ latex: "" }).run()}><Sigma size={14} /></ToolbarBtn>
              <ToolbarBtn title="公式块（$$...$$）" onClick={() => editor.chain().focus().insertBlockMath({ latex: "" }).run()}><Radical size={14} /></ToolbarBtn>
              {supportsImages && (
                <>
                  <span className="composer-toolbar-sep" />
                  <ToolbarBtn title="添加图片（也可直接粘贴/拖入）" onClick={() => fileRef.current?.click()}><ImagePlus size={14} /></ToolbarBtn>
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
            aria-label="停止生成"
            title="停止生成"
          >
            <Square size={18} fill="currentColor" />
          </button>
        ) : (
          <button
            className="composer-btn send"
            onClick={() => doSendRef.current()}
            disabled={editorState?.isEmpty ?? true}
            aria-label="发送"
            title="发送"
          >
            <ArrowUp size={20} />
          </button>
        )}
      </div>
      <div className="composer-hint">Enter 发送 · Shift + Enter 换行 · 支持 Markdown</div>
      <ImagePreview src={preview} onClose={() => setPreview(null)} />
    </div>
  );
}
