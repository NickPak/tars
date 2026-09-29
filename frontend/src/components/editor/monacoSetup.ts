/**
 * Monaco 本地装配（独立 chunk，仅在打开编辑器时加载）：
 * - editor.api 核心 + 选择性 contrib（查找/折叠/多光标等"VSCode 手感"项）
 * - 只配 editor.worker：不需要语言智能服务（TS 诊断那套重货）
 * - 语言按需注册 monarch 高亮定义（0.57 起位于 languages/definitions）
 * - 暗色主题对齐应用调色板
 */
import * as monaco from "monaco-editor/editor/editor.api";
import EditorWorker from "monaco-editor/editor/editor.worker?worker";

// ---- worker：Monaco 后台任务（缩进检测/大文件处理 + json 语言服务）----
self.MonacoEnvironment = {
  getWorker: (_moduleId: string, label: string) =>
    label === "json" ? new JsonWorker() : new EditorWorker(),
};

// ---- contrib（编辑器行为件，不含语言服务）----
import "monaco-editor/editor/contrib/find/browser/findController.js";
import "monaco-editor/editor/contrib/folding/browser/folding.js";
import "monaco-editor/editor/contrib/bracketMatching/browser/bracketMatching.js";
import "monaco-editor/editor/contrib/multicursor/browser/multicursor.js";
import "monaco-editor/editor/contrib/clipboard/browser/clipboard.js";
import "monaco-editor/editor/contrib/contextmenu/browser/contextmenu.js";
import "monaco-editor/editor/contrib/comment/browser/comment.js";
import "monaco-editor/editor/contrib/indentation/browser/indentation.js";
import "monaco-editor/editor/contrib/lineSelection/browser/lineSelection.js";
import "monaco-editor/editor/contrib/linesOperations/browser/linesOperations.js";
import "monaco-editor/editor/contrib/cursorUndo/browser/cursorUndo.js";
import "monaco-editor/editor/contrib/wordOperations/browser/wordOperations.js";
import "monaco-editor/editor/contrib/links/browser/links.js";
import "monaco-editor/editor/contrib/hover/browser/hoverContribution.js";
import "monaco-editor/editor/contrib/smartSelect/browser/smartSelect.js";
import "monaco-editor/editor/contrib/stickyScroll/browser/stickyScrollContribution.js";
import "monaco-editor/editor/contrib/unusualLineTerminators/browser/unusualLineTerminators.js";
import "monaco-editor/editor/contrib/readOnlyMessage/browser/contribution.js";

// ---- 语言高亮（monarch 定义）----
// 必须显式静态导入：模板动态 import（`.../${lang}/register.js`）在
// rolldown/vite 下产出的 chunk 在运行时静默 404——注册从未发生，
// 编辑器表现为"无语法高亮"。静态导入进本 chunk；各语言的 tokenizer
// 本体仍由 register 的 loader 按需懒加载。
import "monaco-editor/languages/definitions/go/register.js";
import "monaco-editor/languages/definitions/typescript/register.js";
import "monaco-editor/languages/definitions/javascript/register.js";
import "monaco-editor/languages/definitions/python/register.js";
import "monaco-editor/languages/definitions/yaml/register.js";
import "monaco-editor/languages/definitions/markdown/register.js";
import "monaco-editor/languages/definitions/shell/register.js";
import "monaco-editor/languages/definitions/bat/register.js";
import "monaco-editor/languages/definitions/powershell/register.js";
import "monaco-editor/languages/definitions/sql/register.js";
import "monaco-editor/languages/definitions/rust/register.js";
import "monaco-editor/languages/definitions/java/register.js";
import "monaco-editor/languages/definitions/cpp/register.js";
import "monaco-editor/languages/definitions/csharp/register.js";
import "monaco-editor/languages/definitions/html/register.js";
import "monaco-editor/languages/definitions/css/register.js";
import "monaco-editor/languages/definitions/xml/register.js";
import "monaco-editor/languages/definitions/ini/register.js";
import "monaco-editor/languages/definitions/dockerfile/register.js";
import "monaco-editor/languages/definitions/lua/register.js";
import "monaco-editor/languages/definitions/ruby/register.js";
import "monaco-editor/languages/definitions/php/register.js";
// json 不在 definitions 里（0.57 起它有完整语言服务 + 独立 worker）
import "monaco-editor/language/json/monaco.contribution.js";
import JsonWorker from "monaco-editor/language/json/json.worker?worker";

// ---- 暗色主题（对齐应用调色板）----
monaco.editor.defineTheme("tars-dark", {
  base: "vs-dark",
  inherit: true,
  rules: [],
  colors: {
    "editor.background": "#131314",
    "editor.lineHighlightBackground": "#1e1f20",
    "editorLineNumber.foreground": "#5f6368",
    "editorCursor.foreground": "#a8c7fa",
    "editor.selectionBackground": "#a8c7fa33",
  },
});

// ---- 扩展名 → 语言 ----
const EXT_MAP: Record<string, string> = {
  go: "go", ts: "typescript", tsx: "typescript", js: "javascript", jsx: "javascript",
  mjs: "javascript", cjs: "javascript", py: "python", json: "json", yaml: "yaml",
  yml: "yaml", md: "markdown", sh: "shell", bash: "shell", zsh: "shell",
  bat: "bat", cmd: "bat", ps1: "powershell", sql: "sql", rs: "rust",
  java: "java", c: "cpp", h: "cpp", cc: "cpp", cpp: "cpp", cxx: "cpp", hpp: "cpp",
  cs: "csharp", html: "html", htm: "html", css: "css", xml: "xml", svg: "xml",
  toml: "ini", ini: "ini", cfg: "ini", dockerfile: "dockerfile", lua: "lua",
  rb: "ruby", php: "php", txt: "plaintext", log: "plaintext",
};

export function languageOf(path: string): string {
  const base = path.toLowerCase();
  const name = base.split(/[\\/]/).pop() ?? base;
  if (name === "dockerfile") return "dockerfile";
  const dot = name.lastIndexOf(".");
  if (dot < 0) return "plaintext";
  return EXT_MAP[name.slice(dot + 1)] ?? "plaintext";
}

export function createEditor(
  container: HTMLDivElement,
  opts: { value: string; language: string; readOnly: boolean },
): monaco.editor.IStandaloneCodeEditor {
  return monaco.editor.create(container, {
    value: opts.value,
    language: opts.language,
    readOnly: opts.readOnly,
    theme: "tars-dark",
    automaticLayout: true,
    // minimap：代码缩略图导航（Monarch 高亮着色，随主题）
    minimap: { enabled: true, maxColumn: 80 },
    fontSize: 13,
    fontFamily: "'Cascadia Code', Consolas, 'JetBrains Mono', monospace",
    lineNumbers: "on",
    scrollBeyondLastLine: false,
    renderWhitespace: "none",
    padding: { top: 8, bottom: 8 },
  });
}
