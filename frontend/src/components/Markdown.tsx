import { useEffect, useRef, useState, isValidElement } from "react";
import type { ReactElement, ReactNode } from "react";
import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";
import remarkMath from "remark-math";
import rehypeKatex from "rehype-katex";
import rehypeHighlight from "rehype-highlight";
import { Copy, Check, Terminal } from "lucide-react";
import "katex/dist/katex.min.css";
import "highlight.js/styles/github-dark.css";
// 浅色主题下的高亮覆盖（选择器带 [data-theme="light"] 作用域，优先级天然更高）
import "../styles/hljs-light.css";

interface MarkdownProps {
  content: string;
}

/**
 * 修复 CJK 全角括号/引号与 ** 相邻时加粗失效的问题（仅处理非代码段）。
 * CommonMark 定界符规则：闭符 ** 前是标点、后是非空白非标点（如
 * `**基于云原生（cloud-native）**设计` 中 `）**设`）不能闭合强调；
 * 开符 ** 后是标点、前是非空白非标点（如 `是**（云原生）**`）同理不能
 * 开启。修复：在 ** 与全角括号/引号之间插入零宽空格（ZWSP 既非空白也
 * 非标点，定界符两侧分类随之合法），视觉与排版完全无损。
 * 仅针对括号/引号类——句读（，。等）本就不造成失效，不污染文本。
 */
const CJK_BRACKETS = "（）【】《》「」『』〔〕〖〗〈〉";
const BOLD_BEFORE_BRACKET = new RegExp(`(\\*\\*)(?=[${CJK_BRACKETS}])`, "g");
const BRACKET_BEFORE_BOLD = new RegExp(`([${CJK_BRACKETS}])(?=\\*\\*)`, "g");

function normalizeCjkBold(text: string): string {
  return text
    .replace(BOLD_BEFORE_BRACKET, "$1​")
    .replace(BRACKET_BEFORE_BOLD, "$1​");
}

/** 按代码段（围栏代码块 + 行内代码）切分，仅对文本段应用 fn */
function mapOutsideCode(content: string, fn: (text: string) => string): string {
  return content
    .split(/(```[\s\S]*?(?:```|$)|`[^`\n]*`)/g)
    .map((seg, i) => (i % 2 === 1 ? seg : fn(seg)))
    .join("");
}

export default function Markdown({ content }: MarkdownProps) {
  return (
    <div className="markdown">
      <ReactMarkdown
        remarkPlugins={[remarkGfm, remarkMath]}
        rehypePlugins={[rehypeKatex, rehypeHighlight]}
        components={{ pre: CodeBlock }}
      >
        {mapOutsideCode(content, normalizeCjkBold)}
      </ReactMarkdown>
    </div>
  );
}

/** 收集节点的全部文本内容（rehype-highlight 会把代码切成多层 span 嵌套） */
function extractText(node: ReactNode): string {
  if (node == null || typeof node === "boolean") return "";
  if (typeof node === "string" || typeof node === "number") return String(node);
  if (Array.isArray(node)) return node.map(extractText).join("");
  if (isValidElement<{ children?: ReactNode }>(node)) {
    return extractText(node.props.children);
  }
  return "";
}

/** 代码块：右上角悬浮"语言标签 + 复制按钮"，hover 代码块时显示 */
function CodeBlock(props: { children?: ReactNode }) {
  const [copied, setCopied] = useState(false);

  const child = Array.isArray(props.children)
    ? props.children[0]
    : props.children;
  const codeEl = child as ReactElement<{
    className?: string;
    children?: ReactNode;
  }> | null;

  // 从 <code className="language-xxx"> 提取语言名
  const className = codeEl?.props?.className ?? "";
  const language = /language-(\w+)/.exec(className)?.[1] ?? "";
  const text = extractText(codeEl?.props?.children).replace(/\n$/, "");

  const handleCopy = () => {
    if (!text) return;
    navigator.clipboard.writeText(text).then(() => {
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    });
  };

  // mermaid 代码块：渲染为图（失败回退源码 + 错误提示）
  if (language === "mermaid") {
    return (
      <div className="codeblock">
        <div className="codeblock-header">
          <span className="codeblock-lang">mermaid</span>
          <button
            className="codeblock-copy"
            onClick={handleCopy}
            title="复制源码到剪贴板"
            aria-label="复制源码"
          >
            {copied ? <Check size={13} /> : <Copy size={13} />}
            {copied ? "已复制" : "复制"}
          </button>
        </div>
        <MermaidDiagram code={text} />
      </div>
    );
  }

  return (
    <div className="codeblock">
      <div className="codeblock-header">
        <span className="codeblock-lang">
          {language === "bash" || language === "sh" || language === "shell" ? (
            <Terminal size={12} />
          ) : null}
          {language || "code"}
        </span>
        <button
          className="codeblock-copy"
          onClick={handleCopy}
          title="复制到剪贴板"
          aria-label="复制代码"
        >
          {copied ? <Check size={13} /> : <Copy size={13} />}
          {copied ? "已复制" : "复制"}
        </button>
      </div>
      <pre className="codeblock-pre">{props.children}</pre>
    </div>
  );
}

// mermaid.initialize 是全局一次性配置——记录已初始化的主题，
// 应用主题切换时按新主题重新初始化。
let mermaidThemeInited: string | null = null;

/**
 * mermaid 代码块 → SVG 图（懒加载 mermaid.js，不进主 bundle）。
 * 流式场景下 code 逐帧变化：300ms 防抖，文本稳定后才渲染。
 * 渲染容器 div 常驻挂载（即使出错）——流式中途的部分代码解析失败是
 * 常态，若容器随错误态卸载，后续 code 到达时 ref 为空、渲染被跳过，
 * 错误态将永远无法自愈。语法错误时隐藏图、回退显示源码 + 错误提示。
 */
function MermaidDiagram({ code }: { code: string }) {
  const ref = useRef<HTMLDivElement>(null);
  const [error, setError] = useState<string | null>(null);
  // 应用主题切换时重渲染（mermaid 主题在 initialize 时锁定）
  const [themeTick, setThemeTick] = useState(0);
  useEffect(() => {
    const onTheme = () => setThemeTick((t) => t + 1);
    window.addEventListener("tars:theme-change", onTheme);
    return () => window.removeEventListener("tars:theme-change", onTheme);
  }, []);

  useEffect(() => {
    if (!code.trim()) return;
    let cancelled = false;
    const timer = setTimeout(async () => {
      const { default: mermaid } = await import("mermaid");
      if (cancelled) return;
      // 主题变化时需要重新 initialize（mermaid 全局只认最近一次配置）
      const mmdTheme = document.documentElement.dataset.theme === "light" ? "default" : "dark";
      if (mermaidThemeInited !== mmdTheme) {
        mermaid.initialize({ startOnLoad: false, theme: mmdTheme, securityLevel: "strict" });
        mermaidThemeInited = mmdTheme;
      }
      // 渲染 ID 唯一化；mermaid.render 失败时会在 body 残留错误占位元素，需清理
      const id = `mmd-${Math.random().toString(36).slice(2)}`;
      try {
        await mermaid.parse(code); // 先校验：parse 失败不产生任何 DOM 残留
        const { svg } = await mermaid.render(id, code);
        if (cancelled || !ref.current) return;
        ref.current.innerHTML = svg;
        setError(null);
      } catch (e) {
        document.getElementById(id)?.remove();
        if (!cancelled) {
          if (ref.current) ref.current.innerHTML = ""; // 清掉上一版成功的图，避免新旧混淆
          setError(e instanceof Error ? e.message : String(e));
        }
      }
    }, 300);
    return () => {
      cancelled = true;
      clearTimeout(timer);
    };
  }, [code, themeTick]);

  return (
    <>
      <div
        ref={ref}
        className="mermaid-diagram"
        style={error ? { display: "none" } : undefined}
      />
      {error && (
        <>
          <div className="mermaid-error">图表渲染失败（语法错误），显示源码</div>
          <pre className="codeblock-pre">{code}</pre>
        </>
      )}
    </>
  );
}
