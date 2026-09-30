import { useState } from "react";
import { Check, Moon, Sun } from "lucide-react";
import { currentTheme, setTheme, type ThemeId } from "../theme/theme";

/**
 * 外观设置页：主题预设切换（点击立即生效，不参与表单保存流程）。
 * 主题卡用真实调色板微缩预览（swatch 的三段色 = 背景/侧栏/accent）。
 */
export default function AppearancePage() {
  const [theme, setThemeState] = useState<ThemeId>(currentTheme());
  const choose = (id: ThemeId) => {
    setThemeState(id);
    void setTheme(id); // 本窗口立即生效 + LS 持久化 + 广播其他窗口
  };

  const cards: { id: ThemeId; label: string; icon: React.ReactNode; swatch: [string, string, string] }[] = [
    { id: "dark", label: "深色", icon: <Moon size={16} />, swatch: ["#131314", "#1e1f20", "#a8c7fa"] },
    { id: "light", label: "浅色", icon: <Sun size={16} />, swatch: ["#ffffff", "#f0f4f9", "#0b57d0"] },
  ];

  return (
    <div className="settings-section">
      <h3 className="settings-section-title">主题</h3>
      <p className="settings-section-desc">
        切换后立即应用到所有窗口（画板、代码编辑器、Mermaid 图同步跟随）。
      </p>
      <div className="theme-cards">
        {cards.map((c) => (
          <button
            key={c.id}
            className={`theme-card${theme === c.id ? " active" : ""}`}
            onClick={() => choose(c.id)}
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
  );
}
