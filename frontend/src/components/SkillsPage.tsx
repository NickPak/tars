/**
 * 技能管理页（设置面板 skills 页签）：安装/卸载技能，即时生效。
 * 与 draft/baseline 保存模型无关——安装即写盘并重跑索引，下一轮对话
 * 的 system 消息即包含新技能目录。
 */
import { useEffect, useState } from "react";
import { Download, PackageOpen, RefreshCw, Shield, Trash2 } from "lucide-react";
import { agentApi } from "../services/agentApi";
import type { AppConfig, Skill } from "../types";

function errText(e: unknown): string {
  return e instanceof Error ? e.message : String(e);
}

/** 内置推荐分类的中文标签（与后端 skills.RecommendedCategories 对应） */
const CATEGORY_LABELS: Record<string, string> = {
  documents: "文档",
  office: "办公",
  devops: "运维部署",
  development: "软件开发",
  data: "数据处理",
  research: "研究检索",
  system: "系统操作",
  design: "设计图像",
  writing: "写作内容",
};

function categoryLabel(c: string): string {
  return CATEGORY_LABELS[c] ?? c;
}

export default function SkillsPage({
  draft,
  update,
}: {
  draft: AppConfig;
  update: (fn: (d: AppConfig) => AppConfig) => void;
}) {
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
      setNotice(`已安装技能 "${name}"`);
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
      setNotice(`已卸载技能 "${name}"`);
      await refresh();
    } catch (e) {
      setError(errText(e));
    }
  };

  const setTier = (key: "tierFullMax" | "tierResidentMax", v: number) => {
    update((d) => ({
      ...d,
      skills: { ...d.skills, [key]: v },
    }));
  };

  return (
    <div className="settings-page">
      <h2 className="settings-page-title">技能（Skills）</h2>
      <p className="settings-page-desc">
        技能是领域知识与专项操作流程的能力层：目录索引常驻系统消息，模型按需
        load_skill 加载完整手册。安装后下一次对话即可用。
      </p>

      {/* 索引档位阈值（走 config 保存流） */}
      <section className="settings-section">
        <div className="settings-section-title">索引档位阈值</div>
        <div className="settings-field">
          <div className="settings-field-copy">
            <span className="settings-field-label">全量清单上限</span>
            <span className="settings-field-hint">
              不超过此数量时，所有技能的 name + 描述常驻系统消息（默认 50）。
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
            <span className="settings-field-label">常驻索引上限</span>
            <span className="settings-field-hint">
              超过此数量时不再常驻清单，只留"用 discover_tools 检索"提示（默认 500）。
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
        <p className="settings-field-hint">
          技能数量超过阈值时，索引从"全量清单"降级为"类别目录"再到"仅检索提示"，
          控制常驻系统消息的大小。保存后下一次对话立即生效。
        </p>
      </section>

      {/* 安装区 */}
      <section className="settings-section">
        <div className="settings-section-title">安装技能</div>
        {installPath ? (
          <div className="skills-install-row">
            <code className="skills-install-path">{installPath}</code>
            <select
              className="settings-select"
              value={pendingCategory}
              onChange={(e) => setPendingCategory(e.target.value)}
            >
              <option value="">分类：未分类（misc）</option>
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
                {installing ? "安装中…" : "覆盖安装"}
              </button>
            ) : (
              <button
                className="dialog-btn primary"
                disabled={installing}
                onClick={() => void doInstall(false)}
              >
                {installing ? "安装中…" : "安装"}
              </button>
            )}
            <button
              className="dialog-btn secondary"
              disabled={installing}
              onClick={clearInstall}
            >
              取消
            </button>
          </div>
        ) : (
          <div className="skills-install-row">
            <button
              className="dialog-btn secondary"
              onClick={() => void pickInstallFile()}
            >
              <Download size={14} />
              选择文件
            </button>
            <button
              className="dialog-btn secondary"
              onClick={() => void pickInstallDir()}
            >
              <Download size={14} />
              选择目录
            </button>
            <span className="skills-pick-hint">
              支持 SKILL.md、.zip、.tar.gz，或含 SKILL.md 的目录
            </span>
          </div>
        )}
        {error && <div className="settings-error">{error}</div>}
        {notice && <div className="settings-saved">{notice}</div>}
      </section>

      {/* 已安装列表 */}
      <section className="settings-section">
        <div className="settings-section-title">
          已安装（{skills?.length ?? 0}）
          <button
            className="skills-refresh"
            title="刷新"
            onClick={() => void refresh()}
          >
            <RefreshCw size={13} />
          </button>
        </div>

        {loading && <div className="settings-loading">加载中…</div>}
        {!loading && skills && skills.length === 0 && (
          <div className="skills-empty">
            <PackageOpen size={20} />
            <span>还没有安装技能。上方选择制品即可安装。</span>
          </div>
        )}
        {!loading &&
          skills?.map((sk) => (
            <div key={sk.name} className="skill-item">
              <div className="skill-item-main">
                <div className="skill-item-head">
                  <code className="skill-item-name">{sk.name}</code>
                  {sk.category && (
                    <span className="skill-item-tag">{sk.category}</span>
                  )}
                  {sk.hasScripts && (
                    <span className="skill-item-tag skill-item-tag-script" title="含可执行脚本">
                      <Shield size={11} />
                      含脚本
                    </span>
                  )}
                </div>
                <p className="skill-item-desc">{sk.description}</p>
              </div>
              <button
                className="skill-item-remove"
                title="卸载"
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
