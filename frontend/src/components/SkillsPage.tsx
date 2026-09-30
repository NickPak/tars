/**
 * 技能管理页（设置面板 skills 页签）：安装/卸载技能，即时生效。
 * 与 draft/baseline 保存模型无关——安装即写盘并重跑索引，下一轮对话
 * 的 system 消息即包含新技能目录。
 */
import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import {
  Download,
  PackageOpen,
  RefreshCw,
  Search,
  Shield,
  Trash2,
} from "lucide-react";
import { agentApi } from "../services/agentApi";
import type { AppConfig, Skill } from "../types";

function errText(e: unknown): string {
  return e instanceof Error ? e.message : String(e);
}

/** 内置推荐分类的标签键（与后端 skills.RecommendedCategories 对应） */
const CATEGORY_KEYS = [
  "documents", "office", "devops", "development", "data",
  "research", "system", "design", "writing",
];

export default function SkillsPage({
  draft,
  update,
}: {
  draft: AppConfig;
  update: (fn: (d: AppConfig) => AppConfig) => void;
}) {
  const { t } = useTranslation();
  // 分类标签：内置分类走字典，自定义分类显示原值
  const categoryLabel = (c: string): string =>
    CATEGORY_KEYS.includes(c) ? t(`skills.cat.${c}`) : c;
  const [skills, setSkills] = useState<Skill[] | null>(null);
  const [categories, setCategories] = useState<string[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);

  const refresh = async () => {
    try {
      const [s, c] = await Promise.all([
        agentApi.listSkills(),
        agentApi.skillCategories(),
      ]);
      setSkills(s);
      setCategories(c);
      setError(null);
    } catch (e) {
      setError(errText(e));
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    void refresh();
  }, []);

  const [installing, setInstalling] = useState(false);
  const [pendingCategory, setPendingCategory] = useState("");
  const [installPath, setInstallPath] = useState("");
  // conflict：同名技能已存在，等待用户选择"覆盖安装"或"取消"
  const [conflict, setConflict] = useState(false);

  const clearInstall = () => {
    setInstallPath("");
    setPendingCategory("");
    setConflict(false);
    setError(null);
  };

  const doInstall = async (overwrite: boolean) => {
    if (!installPath) return;
    setInstalling(true);
    setError(null);
    setNotice(null);
    try {
      const name = await agentApi.installSkill(
        installPath,
        pendingCategory.trim(),
        overwrite,
      );
      setNotice(t("skills.installed", { name }));
      setInstallPath("");
      setPendingCategory("");
      setConflict(false);
      await refresh();
    } catch (e) {
      const msg = errText(e);
      setError(msg);
      // 同名冲突：提示覆盖入口
      if (msg.includes("already installed")) {
        setConflict(true);
      }
    } finally {
      setInstalling(false);
    }
  };

  const pickInstallFile = async () => {
    const path = await agentApi.openSkillFileDialog();
    if (path) {
      setInstallPath(path);
      setConflict(false);
    }
  };

  const pickInstallDir = async () => {
    const path = await agentApi.openSkillDirDialog();
    if (path) {
      setInstallPath(path);
      setConflict(false);
    }
  };

  const doUninstall = async (name: string) => {
    setError(null);
    setNotice(null);
    try {
      await agentApi.uninstallSkill(name);
      setNotice(t("skills.uninstalled", { name }));
      await refresh();
    } catch (e) {
      setError(errText(e));
    }
  };

  const changeCategory = async (name: string, category: string) => {
    setError(null);
    setNotice(null);
    try {
      await agentApi.setSkillCategory(name, category);
      setNotice(t("skills.categoryUpdated", { name }));
      await refresh();
    } catch (e) {
      setError(errText(e));
    }
  };

  const toggleEnabled = async (name: string, enabled: boolean) => {
    setError(null);
    setNotice(null);
    try {
      await agentApi.setSkillEnabled(name, enabled);
      setNotice(
        enabled ? t("skills.enabled", { name }) : t("skills.disabled", { name }),
      );
      await refresh();
    } catch (e) {
      setError(errText(e));
    }
  };

  const setTier = (
    key: "tierFullMax" | "tierResidentMax" | "discoverResultLimit",
    v: number,
  ) => {
    update((d) => ({
      ...d,
      skills: { ...d.skills, [key]: v },
    }));
  };

  // 模糊搜索：与模型侧 discover_tools 同款 BM25 检索和候选数上限，
  // 页面所见 = 模型所得（调试检索精确度用）。防抖 300ms；清空恢复完整列表。
  const [query, setQuery] = useState("");
  const [searchResults, setSearchResults] = useState<Skill[] | null>(null);

  useEffect(() => {
    const q = query.trim();
    if (!q) {
      setSearchResults(null);
      return;
    }
    const timer = setTimeout(() => {
      void (async () => {
        try {
          setSearchResults(await agentApi.searchSkills(q));
        } catch (e) {
          setError(errText(e));
        }
      })();
    }, 300);
    return () => clearTimeout(timer);
  }, [query]);

  const displayed = searchResults ?? skills;

  return (
    <div className="settings-page">
      <h2 className="settings-page-title">{t("skills.title")}</h2>
      <p className="settings-page-desc">
        {t("skills.desc")}
      </p>

      {/* 索引档位阈值（走 config 保存流） */}
      <section className="settings-section">
        <div className="settings-section-title">{t("skills.thresholds")}</div>
        <div className="settings-field">
          <div className="settings-field-copy">
            <span className="settings-field-label">{t("skills.fullListCap")}</span>
            <span className="settings-field-hint">
              {t("skills.fullListCapHint")}
            </span>
          </div>
          <div className="settings-field-control">
            <input
              type="number"
              min={1}
              max={500}
              className="settings-input"
              value={draft.skills.tierFullMax}
              onChange={(e) => setTier("tierFullMax", Number(e.target.value))}
            />
          </div>
        </div>
        <div className="settings-field">
          <div className="settings-field-copy">
            <span className="settings-field-label">{t("skills.indexCap")}</span>
            <span className="settings-field-hint">
              {t("skills.indexCapHint")}
            </span>
          </div>
          <div className="settings-field-control">
            <input
              type="number"
              min={1}
              max={2000}
              className="settings-input"
              value={draft.skills.tierResidentMax}
              onChange={(e) => setTier("tierResidentMax", Number(e.target.value))}
            />
          </div>
        </div>
        <div className="settings-field">
          <div className="settings-field-copy">
            <span className="settings-field-label">{t("skills.searchCap")}</span>
            <span className="settings-field-hint">
              {t("skills.searchCapHint")}
            </span>
          </div>
          <div className="settings-field-control">
            <input
              type="number"
              min={1}
              max={50}
              className="settings-input"
              value={draft.skills.discoverResultLimit}
              onChange={(e) =>
                setTier("discoverResultLimit", Number(e.target.value))
              }
            />
          </div>
        </div>
        <p className="settings-field-hint">
          {t("skills.thresholdNote")}
        </p>
      </section>

      {/* 安装区 */}
      <section className="settings-section">
        <div className="settings-section-title">{t("skills.install")}</div>
        {installPath ? (
          <div className="skills-install-row">
            <code className="skills-install-path">{installPath}</code>
            <select
              className="settings-select"
              value={pendingCategory}
              onChange={(e) => setPendingCategory(e.target.value)}
            >
              <option value="">{t("skills.categoryMisc")}</option>
              {categories.map((c) => (
                <option key={c} value={c}>
                  {categoryLabel(c)}
                </option>
              ))}
            </select>
            {conflict ? (
              <button
                className="dialog-btn danger"
                disabled={installing}
                onClick={() => void doInstall(true)}
              >
                {installing ? t("skills.installing") : t("skills.overwriteInstall")}
              </button>
            ) : (
              <button
                className="dialog-btn primary"
                disabled={installing}
                onClick={() => void doInstall(false)}
              >
                {installing ? t("skills.installing") : t("skills.installBtn")}
              </button>
            )}
            <button
              className="dialog-btn secondary"
              disabled={installing}
              onClick={clearInstall}
            >
              {t("common.cancel")}
            </button>
          </div>
        ) : (
          <div className="skills-install-row">
            <button
              className="dialog-btn secondary"
              onClick={() => void pickInstallFile()}
            >
              <Download size={14} />
              {t("skills.chooseFile")}
            </button>
            <button
              className="dialog-btn secondary"
              onClick={() => void pickInstallDir()}
            >
              <Download size={14} />
              {t("skills.chooseDir")}
            </button>
            <span className="skills-pick-hint">
              {t("skills.artifactHint")}
            </span>
          </div>
        )}
        {error && <div className="settings-error">{error}</div>}
        {notice && <div className="settings-saved">{notice}</div>}
      </section>

      {/* 已安装列表 */}
      <section className="settings-section">
        <div className="settings-section-title skills-list-title">
          {t("skills.installedList", { count: displayed?.length ?? 0 })}
          {searchResults ? ` / ${skills?.length ?? 0}` : ""}
          <span className="skills-title-actions">
            <span className="skills-search">
              <Search size={12} />
              <input
                value={query}
                onChange={(e) => setQuery(e.target.value)}
                placeholder={t("skills.searchPlaceholder")}
                spellCheck={false}
              />
            </span>
            <button
              className="skills-refresh"
              title={t("skills.refresh")}
              onClick={() => void refresh()}
            >
              <RefreshCw size={13} />
            </button>
          </span>
        </div>

        {loading && <div className="settings-loading">{t("settings.loading")}</div>}
        {!loading && searchResults && searchResults.length === 0 && (
          <div className="skills-empty">
            <PackageOpen size={20} />
            <span>{t("skills.noMatch")}</span>
          </div>
        )}
        {!loading && !searchResults && skills && skills.length === 0 && (
          <div className="skills-empty">
            <PackageOpen size={20} />
            <span>{t("skills.noneInstalled")}</span>
          </div>
        )}
        {!loading &&
          displayed?.map((sk) => (
            <div
              key={sk.name}
              className={`skill-item${sk.enabled ? "" : " disabled"}`}
            >
              <div className="skill-item-main">
                <div className="skill-item-head">
                  <code className="skill-item-name">{sk.name}</code>
                  <button
                    className={`switch${sk.enabled ? " on" : ""}`}
                    role="switch"
                    aria-checked={sk.enabled}
                    title={
                      sk.enabled
                        ? t("skills.disableTip")
                        : t("skills.enableTip")
                    }
                    onClick={() => void toggleEnabled(sk.name, !sk.enabled)}
                  >
                    <span className="switch-thumb" />
                  </button>
                  <select
                    className="skill-item-category"
                    value={sk.category || "misc"}
                    onChange={(e) => void changeCategory(sk.name, e.target.value)}
                    title={t("skills.editCategoryTip")}
                  >
                    <option value="misc">{t("skills.misc")}</option>
                    {categories
                      .filter((c) => c !== "misc")
                      .map((c) => (
                        <option key={c} value={c}>
                          {categoryLabel(c)}
                        </option>
                      ))}
                  </select>
                  {sk.hasScripts && (
                    <span className="skill-item-tag skill-item-tag-script" title={t("skills.hasScriptTip")}>
                      <Shield size={11} />
                      {t("skills.hasScript")}
                    </span>
                  )}
                </div>
                <p className="skill-item-desc">{sk.description}</p>
              </div>
              <button
                className="skill-item-remove"
                title={t("skills.uninstall")}
                onClick={() => void doUninstall(sk.name)}
              >
                <Trash2 size={14} />
              </button>
            </div>
          ))}
      </section>
    </div>
  );
}
