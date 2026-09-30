import { useState } from "react";
import { useTranslation } from "react-i18next";
import { Check, Moon, Sun, Languages } from "lucide-react";
import { currentTheme, setTheme, type ThemeId } from "../theme/theme";
import { currentLocalePref, setLocalePref, type LocalePref } from "../i18n";

/**
 * 外观设置页：主题预设 + 界面语言（点击立即生效，不参与表单保存流程）。
 * 主题卡用真实调色板微缩预览（swatch 的三段色 = 背景/侧栏/accent）。
 */
export default function AppearancePage() {
  const { t } = useTranslation();
  const [theme, setThemeState] = useState<ThemeId>(currentTheme());
  const [locale, setLocaleState] = useState<LocalePref>(currentLocalePref());

  const chooseTheme = (id: ThemeId) => {
    setThemeState(id);
    void setTheme(id); // 本窗口立即生效 + LS 持久化 + 广播其他窗口
  };
  const chooseLocale = (pref: LocalePref) => {
    setLocaleState(pref);
    void setLocalePref(pref);
  };

  const themeCards: { id: ThemeId; label: string; icon: React.ReactNode; swatch: [string, string, string] }[] = [
    { id: "dark", label: t("appearance.dark"), icon: <Moon size={16} />, swatch: ["#131314", "#1e1f20", "#a8c7fa"] },
    { id: "light", label: t("appearance.light"), icon: <Sun size={16} />, swatch: ["#ffffff", "#f0f4f9", "#0b57d0"] },
  ];
  const localeCards: { id: LocalePref; label: string }[] = [
    { id: "", label: t("appearance.langAuto") },
    { id: "zh", label: t("appearance.langZh") },
    { id: "en", label: t("appearance.langEn") },
  ];

  return (
    <>
      <div className="settings-section">
        <h3 className="settings-section-title">{t("appearance.theme")}</h3>
        <p className="settings-section-desc">{t("appearance.themeDesc")}</p>
        <div className="theme-cards">
          {themeCards.map((c) => (
            <button
              key={c.id}
              className={`theme-card${theme === c.id ? " active" : ""}`}
              onClick={() => chooseTheme(c.id)}
              aria-pressed={theme === c.id}
            >
              <span className="theme-swatch">
                <i style={{ background: c.swatch[0] }} />
                <i style={{ background: c.swatch[1] }} />
                <i style={{ background: c.swatch[2] }} />
              </span>
              <span className="theme-card-label">
                {c.icon}
                {c.label}
              </span>
              {theme === c.id && <Check size={14} className="theme-card-check" />}
            </button>
          ))}
        </div>
      </div>

      <div className="settings-section">
        <h3 className="settings-section-title">{t("appearance.language")}</h3>
        <p className="settings-section-desc">{t("appearance.languageDesc")}</p>
        <div className="theme-cards">
          {localeCards.map((c) => (
            <button
              key={c.id || "auto"}
              className={`theme-card${locale === c.id ? " active" : ""}`}
              onClick={() => chooseLocale(c.id)}
              aria-pressed={locale === c.id}
            >
              <span className="theme-card-label" style={{ padding: "8px 2px" }}>
                <Languages size={16} />
                {c.label}
              </span>
              {locale === c.id && <Check size={14} className="theme-card-check" />}
            </button>
          ))}
        </div>
      </div>
    </>
  );
}
