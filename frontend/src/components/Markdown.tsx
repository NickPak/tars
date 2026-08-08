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

export default function Markdown({ content }: MarkdownProps) {
  return (
    <div className="markdown">
      <ReactMarkdown
        remarkPlugins={[remarkGfm, remarkMath]}
        rehypePlugins={[rehypeKatex, rehypeHighlight]}
        components={{ pre: CodeBlock }}
      >
        {content}
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
