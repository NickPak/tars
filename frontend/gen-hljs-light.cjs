// 一次性生成脚本：highlight.js github.css → 浅色主题作用域覆盖样式
const fs = require("fs");
const raw = fs.readFileSync("node_modules/highlight.js/styles/github.css", "utf8");
const css = raw.replace(/\/\*[\s\S]*?\*\//g, ""); // 先剥注释，避免被当选择器
const scope = ':root[data-theme="light"] ';
const out = css.replace(/(^|\})([^{}]+)\{/gm, (m, sep, sel) => {
  const scoped = sel
    .split(",")
    .map((s) => s.trim())
    .filter(Boolean)
    .map((s) => scope + s)
    .join(", ");
  return sep + scoped + " {";
});
fs.writeFileSync(
  "src/styles/hljs-light.css",
  "/* 由 highlight.js github.css 生成（gen-hljs-light.cjs）：选择器加浅色主题作用域，勿手改 */\n" + out,
);
console.log("generated, bytes:", out.length);
