import { Events } from "@wailsio/runtime";
import { BroadcastTheme } from "../../bindings/tars/configservice";

/**
 * 主题系统：
 * - 单一事实源是 CSS 变量（:root / :root[data-theme="light"]），
 *   切换仅翻转 documentElement 的 data-theme 属性；
 * - 持久化用 localStorage（tars.theme）——同 profile 下所有窗口共享，
 *   启动时同步读取、渲染前应用，零闪烁；
 * - 非 CSS 子系统（Monaco/Mermaid/Excalidraw）监听窗口内
 *   "tars:theme-change" CustomEvent 跟随；
 * - 跨窗口实时同步经后端广播 theme:changed（各窗口共享 localStorage，
 *   广播只负责"立刻生效"，新窗口启动读 LS 自然一致）。
 */

export type ThemeId = "dark" | "light";

export const THEME_LS_KEY = "tars.theme";
export const THEME_WIN_EVENT = "tars:theme-change";

export function currentTheme(): ThemeId {
  return localStorage.getItem(THEME_LS_KEY) === "light" ? "light" : "dark";
}

/** 本窗口应用主题（CSS 变量立即生效，再通知窗口内的 Monaco 等监听者） */
export function applyThemeLocal(id: ThemeId): void {
  document.documentElement.dataset.theme = id;
  window.dispatchEvent(new CustomEvent(THEME_WIN_EVENT, { detail: id }));
}

/** 启动早期调用（渲染前）：读 LS 应用 + 挂跨窗口广播监听 */
export function initTheme(): void {
  applyThemeLocal(currentTheme());
  Events.On("theme:changed", (ev) => {
    const id = String(ev.data) === "light" ? "light" : "dark";
    localStorage.setItem(THEME_LS_KEY, id);
    applyThemeLocal(id);
  });
}

/** 设置页切换主题：本窗口立即生效 + 持久化 + 广播其他窗口 */
export async function setTheme(id: ThemeId): Promise<void> {
  localStorage.setItem(THEME_LS_KEY, id);
  applyThemeLocal(id);
  await BroadcastTheme(id).catch(() => {});
}
