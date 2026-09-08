import { useState, isValidElement } from "react";
import type { ReactElement, ReactNode } from "react";
import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";
import remarkMath from "remark-math";
import rehypeKatex from "rehype-katex";
import rehypeHighlight from "rehype-highlight";
import { Copy, Check, Terminal } from "lucide-react";
import "katex/dist/katex.min.css";
import "highlight.js/styles/github-dark.css";

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
