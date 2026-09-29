import { useEffect, useRef, useState } from "react";

export type Editor = import("monaco-editor").editor.IStandaloneCodeEditor;

/**
 * Monaco 封装（本模块经 React.lazy 引入——monaco 资源不进主 bundle）。
 * 只读查看 / 轻编辑两用；内容变更经 onChange 回传（保存决策在调用方）。
 */
export default function CodeEditor({
  path,
  value,
  readOnly,
  onChange,
  onMount,
}: {
  /** 文件路径（决定语言高亮） */
  path: string;
  value: string;
  readOnly?: boolean;
  onChange?: (v: string) => void;
  onMount?: (editor: Editor) => void;
}) {
  const containerRef = useRef<HTMLDivElement>(null);
  const editorRef = useRef<Editor | null>(null);
  const [ready, setReady] = useState(false);

  useEffect(() => {
    let disposed = false;
    void import("./editor/monacoSetup").then((m) => {
      if (disposed || !containerRef.current) return;
      const editor = m.createEditor(containerRef.current, {
        value,
        language: m.languageOf(path),
        readOnly: readOnly ?? true,
      });
      editorRef.current = editor;
      if (onChange) {
        editor.onDidChangeModelContent(() => onChange(editor.getValue()));
      }
      onMount?.(editor);
      setReady(true);
    });
    return () => {
      disposed = true;
      editorRef.current?.dispose();
      editorRef.current = null;
    };
    // 挂载一次：path/value 变化由父组件换 key 重建
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  return (
    <div className="code-editor">
      {!ready && <div className="code-editor-loading">编辑器加载中…</div>}
      <div ref={containerRef} className="code-editor-host" />
    </div>
  );
}
