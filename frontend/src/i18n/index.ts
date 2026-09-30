import i18n from "i18next";
import { initReactI18next } from "react-i18next";
import { Events } from "@wailsio/runtime";
import { BroadcastLocale } from "../../bindings/tars/configservice";
import zh from "./locales/zh";
import en from "./locales/en";

/**
 * 国际化：i18next + react-i18next 标准方案。
 * - 语言偏好持久化 localStorage（tars.locale）："" = 跟随系统；
 * - 渲染前同步初始化（main.tsx 最早期调用 initI18n），零闪烁；
 * - 跨窗口实时同步复用主题管道（后端 locale:changed 广播）；
 * - 资源内联打包（双语规模小，无需 HTTP 懒加载/命名空间拆分）。
 */

export type LocaleId = "zh" | "en";
/** "" = 跟随系统 */
export type LocalePref = "" | LocaleId;

export const LOCALE_LS_KEY = "tars.locale";
const LOCALE_WIN_EVENT = "tars:locale-change";

/** 解析偏好为实际语言：空偏好跟随系统（navigator.language） */
export function resolveLocale(pref: LocalePref): LocaleId {
  if (pref) return pref;
  return (navigator.language || "").toLowerCase().startsWith("zh") ? "zh" : "en";
}

export function currentLocalePref(): LocalePref {
  const v = localStorage.getItem(LOCALE_LS_KEY);
  return v === "zh" || v === "en" ? v : "";
}

/** 本窗口应用语言（i18next + <html lang> + 窗口内通知） */
function applyLocaleLocal(id: LocaleId): void {
  if (i18n.language !== id) void i18n.changeLanguage(id);
  document.documentElement.lang = id === "zh" ? "zh-CN" : "en";
  window.dispatchEvent(new CustomEvent(LOCALE_WIN_EVENT, { detail: id }));
}

/** 启动早期调用（渲染前）：初始化 + 挂跨窗口广播监听 */
export async function initI18n(): Promise<void> {
  await i18n.use(initReactI18next).init({
    resources: {
      zh: { translation: zh },
      en: { translation: en },
    },
    lng: resolveLocale(currentLocalePref()),
    fallbackLng: "zh",
    interpolation: { escapeValue: false }, // React 已防 XSS
    returnNull: false,
  });
  applyLocaleLocal(i18n.language as LocaleId);
  Events.On("locale:changed", (ev) => {
    const pref = String(ev.data) as LocalePref;
    localStorage.setItem(LOCALE_LS_KEY, pref);
    applyLocaleLocal(resolveLocale(pref));
  });
}

/** 设置页切换语言：本窗口立即生效 + 持久化 + 广播其他窗口 */
export async function setLocalePref(pref: LocalePref): Promise<void> {
  localStorage.setItem(LOCALE_LS_KEY, pref);
  applyLocaleLocal(resolveLocale(pref));
  await BroadcastLocale(pref).catch(() => {});
}

export default i18n;
