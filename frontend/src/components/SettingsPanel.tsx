import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import type { ReactNode } from "react";
import {
  Activity,
  BookMarked,
  Bot,
  Brain,
  Check,
  Eye,
  EyeOff,
  FolderOpen,
  Info,
  Palette,
  Plug,
  SlidersHorizontal,
  Sparkles,
  X,
} from "lucide-react";
import { agentApi } from "../services/agentApi";
import { useChatStore } from "../store/chatStore";
import { useSettingsStore } from "../store/settingsStore";
import type { SettingsTab } from "../store/settingsStore";
import type { AppConfig, LLMConfig, ModelConfig, ProviderConfig } from "../types";
import { ConfirmDialog } from "./Dialog";
import SkillsPage from "./SkillsPage";
import MCPPage from "./MCPPage";
import MemoryPage from "./MemoryPage";
import AppearancePage from "./AppearancePage";

interface NavItem {
  tab: SettingsTab;
  icon: ReactNode;
}

// 标签在渲染时取 t(`settings.nav.${tab}`)（语言切换即时跟随）
const NAV_ITEMS: NavItem[] = [
  { tab: "general", icon: <SlidersHorizontal size={15} /> },
  { tab: "model", icon: <Brain size={15} /> },
  { tab: "agent", icon: <Bot size={15} /> },
  { tab: "trace", icon: <Activity size={15} /> },
  { tab: "memory", icon: <BookMarked size={15} /> },
  { tab: "skills", icon: <Sparkles size={15} /> },
  { tab: "mcp", icon: <Plug size={15} /> },
  { tab: "appearance", icon: <Palette size={15} /> },
  { tab: "about", icon: <Info size={15} /> },
];

/** 有真实内容、显示底部保存条的页签（其余为占位）。
 *  保存条是面板级的：MCP/技能/记忆列表的变更即改即存不进 draft，
 *  但底栏在各页签间保持一致的呈现（按钮仅在有 draft 改动时可用）。 */
const REAL_TABS: SettingsTab[] = ["general", "model", "agent", "trace", "memory", "skills", "mcp"];

function errText(e: unknown): string {
  return e instanceof Error ? e.message : String(e);
}

/**
 * 设置面板：居中模态（左侧分类导航 + 右侧内容区），参考 DeepSeek-Reasonix。
 * 数据流：打开时 GetAppConfig → 本地 draft 编辑 → 保存时 SaveAppConfig
 * 全量提交（后端按键级合并写回 config.yaml，保留注释与 apiKey 引用）。
 */
export default function SettingsPanel() {
  const { t } = useTranslation();
  const open = useSettingsStore((s) => s.open);
  const tab = useSettingsStore((s) => s.tab);
  const setTab = useSettingsStore((s) => s.setTab);
  const closeSettings = useSettingsStore((s) => s.closeSettings);

  const [draft, setDraft] = useState<AppConfig | null>(null);
  const [baseline, setBaseline] = useState<AppConfig | null>(null);
  const [loading, setLoading] = useState(false);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [saved, setSaved] = useState(false);
  const [discardConfirm, setDiscardConfirm] = useState(false);

  // 打开时加载配置
  useEffect(() => {
    if (!open) return;
    setLoading(true);
    setError(null);
    setSaved(false);
    agentApi
      .getAppConfig()
      .then((cfg) => {
        setDraft(cfg);
        setBaseline(cfg);
      })
      .catch((e) => setError(errText(e)))
      .finally(() => setLoading(false));
  }, [open]);

  const dirty =
    draft !== null &&
    baseline !== null &&
    JSON.stringify(draft) !== JSON.stringify(baseline);

  const requestClose = () => {
    if (dirty) {
      setDiscardConfirm(true);
    } else {
      closeSettings();
    }
  };

  // Esc 关闭
  useEffect(() => {
    if (!open) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") requestClose();
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open, dirty]);

  if (!open) return null;

  const update = (fn: (d: AppConfig) => AppConfig) => {
    setDraft((d) => (d ? fn(d) : d));
    setSaved(false);
  };
  const updateLLM = (patch: Partial<LLMConfig>) =>
    update((d) => ({ ...d, llm: { ...d.llm, ...patch } }));

  const handleSave = async () => {
    if (!draft || saving) return;
    setSaving(true);
    setError(null);
    try {
      await agentApi.saveAppConfig(draft);
      // 重新拉取（后端归一化后的生效值，如 apiKey 引用展开结果）
      const fresh = await agentApi.getAppConfig();
      setDraft(fresh);
      setBaseline(fresh);
      setSaved(true);
      setTimeout(() => setSaved(false), 2000);
      // 模型/价格/上下文窗口可能已变：刷新 TopicBar、模型列表与状态栏
      const cs = useChatStore.getState();
      void cs.refreshModelInfo();
      void cs.refreshModels();
      void cs.refreshStats();
    } catch (e) {
      setError(errText(e));
    } finally {
      setSaving(false);
    }
  };

  const showFooter = REAL_TABS.includes(tab) && draft !== null;

  return (
    <div className="settings-overlay" onClick={requestClose}>
      <div className="settings-modal" onClick={(e) => e.stopPropagation()}>
        <div className="settings-head">
          <span className="settings-title">{t("settings.title")}</span>
          <button
            className="settings-close"
            title={t("settings.close")}
            aria-label={t("settings.closeAria")}
            onClick={requestClose}
          >
            <X size={16} />
          </button>
        </div>

        <div className="settings-body">
          <nav className="settings-nav">
            {NAV_ITEMS.map((item) => (
              <button
                key={item.tab}
                className={`settings-nav-item${tab === item.tab ? " active" : ""}`}
                onClick={() => setTab(item.tab)}
              >
                {item.icon}
                <span>{t(`settings.nav.${item.tab}`)}</span>
              </button>
            ))}
          </nav>

          <main className="settings-content">
            {loading && <div className="settings-loading">{t("settings.loading")}</div>}
            {!loading && error && !draft && (
              <div className="settings-load-error">{t("settings.loadFailed", { msg: error })}</div>
            )}
            {!loading && draft && (
              <>
                {tab === "general" && (
                  <GeneralPage draft={draft} update={update} />
                )}
                {tab === "model" && (
                  <ModelPage draft={draft} updateLLM={updateLLM} />
                )}
                {tab === "agent" && (
                  <AgentPage draft={draft} update={update} />
                )}
                {tab === "trace" && (
                  <TracePage draft={draft} update={update} />
                )}
                {tab === "memory" && (
                  <MemoryPage draft={draft} update={update} />
                )}
                {tab === "skills" && (
                  <SkillsPage draft={draft} update={update} />
                )}
                {tab === "mcp" && <MCPPage />}
                {tab === "appearance" && <AppearancePage />}
                {tab === "about" && <AboutPage />}
              </>
            )}
          </main>
        </div>

        {showFooter && (
          <div className="settings-foot">
            {error && <span className="settings-error">{error}</span>}
            {saved && !error && (
              <span className="settings-saved">
                <Check size={13} />
                {t("settings.saved")}
              </span>
            )}
            <span className="settings-foot-spacer" />
            <button
              className="dialog-btn secondary"
              disabled={!dirty || saving}
              onClick={() => setDraft(baseline)}
            >
              {t("settings.discard")}
            </button>
            <button
              className="dialog-btn primary"
              disabled={!dirty || saving}
              onClick={() => void handleSave()}
            >
              {saving ? t("settings.saving") : t("common.save")}
            </button>
          </div>
        )}
      </div>

      <ConfirmDialog
        open={discardConfirm}
        message={t("settings.discardConfirm")}
        onCancel={() => setDiscardConfirm(false)}
        onConfirm={() => {
          setDiscardConfirm(false);
          closeSettings();
        }}
      />
    </div>
  );
}

/* ===== 基础组件：页面壳 / 分组卡片 / 字段行 ===== */

function PageShell({
  title,
  desc,
  children,
}: {
  title: string;
  desc: string;
  children: ReactNode;
}) {
  return (
    <div className="settings-page">
      <h2 className="settings-page-title">{title}</h2>
      <p className="settings-page-desc">{desc}</p>
      {children}
    </div>
  );
}

function Section({ title, children }: { title: string; children: ReactNode }) {
  return (
    <section className="settings-section">
      <div className="settings-section-title">{title}</div>
      {children}
    </section>
  );
}

function Field({
  label,
  hint,
  children,
}: {
  label: string;
  hint?: string;
  children: ReactNode;
}) {
  return (
    <div className="settings-field">
      <div className="settings-field-copy">
        <span className="settings-field-label">{label}</span>
        {hint && <span className="settings-field-hint">{hint}</span>}
      </div>
      <div className="settings-field-control">{children}</div>
    </div>
  );
}

/** 分段选择控件（枚举类选项的首选） */
/** 布尔开关（switch 样式的语义化封装，能力声明等布尔配置项用） */
function BoolSwitch({
  value,
  onChange,
}: {
  value: boolean;
  onChange: (v: boolean) => void;
}) {
  return (
    <button
      className={`switch${value ? " on" : ""}`}
      role="switch"
      aria-checked={value}
      onClick={() => onChange(!value)}
    >
      <span className="switch-thumb" />
    </button>
  );
}

function Seg<T extends string>({
  value,
  options,
  onChange,
  disabled,
}: {
  value: T;
  options: { value: T; label: string }[];
  onChange: (v: T) => void;
  disabled?: boolean;
}) {
  return (
    <div className={`seg${disabled ? " seg-disabled" : ""}`}>
      {options.map((o) => (
        <button
          key={o.value}
          className={`seg-btn${value === o.value ? " on" : ""}`}
          disabled={disabled}
          onClick={() => onChange(o.value)}
        >
          {o.label}
        </button>
      ))}
    </div>
  );
}

/* ===== 各分类页面 ===== */

/** 密钥输入框：默认掩码显示，眼睛按钮切换明文/掩码 */
function SecretInput({
  value,
  placeholder,
  onChange,
}: {
  value: string;
  placeholder?: string;
  onChange: (v: string) => void;
}) {
  const { t } = useTranslation();
  const [show, setShow] = useState(false);
  return (
    <div className="settings-secret">
      <input
        className="settings-input"
        type={show ? "text" : "password"}
        value={value}
        placeholder={placeholder}
        autoComplete="off"
        onChange={(e) => onChange(e.target.value)}
      />
      <button
        className="settings-secret-eye"
        title={show ? t("settings.hideSecret") : t("settings.showSecret")}
        aria-label={show ? t("settings.hideSecret") : t("settings.showSecret")}
        onClick={() => setShow((s) => !s)}
      >
        {show ? <EyeOff size={14} /> : <Eye size={14} />}
      </button>
    </div>
  );
}

function GeneralPage({
  draft,
  update,
}: {
  draft: AppConfig;
  update: (fn: (d: AppConfig) => AppConfig) => void;
}) {
  const { t } = useTranslation();
  const handleBrowse = async () => {
    const dir = await agentApi.openDirectoryDialog();
    if (dir) update((d) => ({ ...d, workDir: dir }));
  };

  return (
    <PageShell title={t("settings.general.title")} desc={t("settings.general.desc")}>
      <Section title={t("settings.general.storage")}>
        <Field
          label={t("settings.general.workDir")}
          hint={t("settings.general.workDirHint")}
        >
          <input
            className="settings-input"
            value={draft.workDir}
            placeholder={t("settings.general.workDirPlaceholder")}
            onChange={(e) =>
              update((d) => ({ ...d, workDir: e.target.value }))
            }
          />
          <button
            className="dialog-btn secondary"
            title={t("settings.general.browse")}
            onClick={() => void handleBrowse()}
          >
            <FolderOpen size={14} />
          </button>
        </Field>
      </Section>
      {/* 界面语言设置在"外观"页（立即生效，不走 draft 保存流程） */}
    </PageShell>
  );
}

/** 供应商类型元信息：各字段的显隐（对应后端原生组件能力）。
 *  品牌名原样显示；带中文后缀的标签与提示走字典键（labelKey/hintKey）。 */
const PROVIDER_TYPES: {
  value: string;
  label?: string;
  labelKey?: string;
  needApiKey: boolean;
  needBaseUrl: boolean;
  hintKey?: string;
  hasRegion: boolean;
  hasCacheTTL: boolean;
}[] = [
  {
    value: "gemini", label: "Gemini", needApiKey: true,
    needBaseUrl: false, hasRegion: false, hasCacheTTL: false,
  },
  {
    value: "openai", labelKey: "settings.model.providerOpenAI", needApiKey: true,
    needBaseUrl: true, hintKey: "settings.model.baseUrlOpenAI",
    hasRegion: false, hasCacheTTL: false,
  },
  {
    value: "claude", label: "Claude", needApiKey: true,
    needBaseUrl: false, hintKey: "settings.model.baseUrlClaude",
    hasRegion: false, hasCacheTTL: true,
  },
  {
    value: "deepseek", label: "DeepSeek", needApiKey: true,
    needBaseUrl: false, hintKey: "settings.model.baseUrlDefault",
    hasRegion: false, hasCacheTTL: false,
  },
  {
    value: "qwen", labelKey: "settings.model.providerQwen", needApiKey: true,
    needBaseUrl: true, hintKey: "settings.model.baseUrlQwen",
    hasRegion: false, hasCacheTTL: false,
  },
  {
    value: "ark", labelKey: "settings.model.providerArk", needApiKey: true,
    needBaseUrl: false, hintKey: "settings.model.baseUrlDefault",
    hasRegion: true, hasCacheTTL: false,
  },
  {
    value: "ollama", labelKey: "settings.model.providerOllama", needApiKey: false,
    needBaseUrl: false, hintKey: "settings.model.baseUrlOllama",
    hasRegion: false, hasCacheTTL: false,
  },
];

function providerMeta(type: string) {
  return PROVIDER_TYPES.find((p) => p.value === type) ?? PROVIDER_TYPES[1];
}

/** 哪些供应商类型的模型条目显示"思考模式"开关 */
const THINKING_SWITCH_TYPES = ["deepseek", "qwen", "ark", "ollama"];

/** 模型页：供应商列表 + 模型列表（多供应商管理） */
function ModelPage({
  draft,
  updateLLM,
}: {
  draft: AppConfig;
  updateLLM: (patch: Partial<LLMConfig>) => void;
}) {
  const { t } = useTranslation();
  // 供应商元信息 → 显示文本（品牌名原样，其余走字典）
  const pLabel = (type: string): string => {
    const m = providerMeta(type);
    return m.labelKey ? t(m.labelKey) : (m.label ?? type);
  };
  const pHint = (type: string): string => {
    const m = providerMeta(type);
    return m.hintKey ? t(m.hintKey) : "";
  };
  const llm = draft.llm;
  // 手风琴展开的条目 key："p:<索引>" 供应商 / "m:<索引>" 模型。
  // 必须用数组索引而非条目 ID 作 key：编辑 ID 时 ID 每击键都变，
  // 若 key 跟随 ID 会导致输入框卸载重建、光标丢失。
  const [expanded, setExpanded] = useState<string | null>(null);
  const toggle = (key: string) =>
    setExpanded((cur) => (cur === key ? null : key));

  const providerTypeOf = (pid: string) =>
    llm.providers.find((p) => p.id === pid)?.type ?? "";

  // --- 供应商增删改 ---
  // 条目 ID 统一由 "供应商ID/模型ID" 自动生成；modelId 为空时 ID 为空
  // （保存时后端校验会提示补全）。
  const genModelId = (provider: string, modelId: string) =>
    modelId ? `${provider}/${modelId}` : "";

  const patchProvider = (idx: number, patch: Partial<ProviderConfig>) => {
    const providers = llm.providers.map((p, i) =>
      i === idx ? { ...p, ...patch } : p,
    );
    const out: Partial<LLMConfig> = { providers };
    // 供应商 ID 变更时级联：引用它的模型条目跟随改 provider 并重新生成条目 ID
    const oldId = llm.providers[idx]?.id;
    const newId = providers[idx]?.id;
    if (patch.id !== undefined && oldId && newId !== oldId) {
      const models = llm.models.map((m) =>
        m.provider !== oldId
          ? m
          : { ...m, provider: newId, entryId: genModelId(newId, m.modelId) },
      );
      out.models = models;
      // 激活条目引用同步
      const activeIdx = llm.models.findIndex((m) => m.entryId === llm.active);
      if (activeIdx >= 0) out.active = models[activeIdx].entryId;
    }
    updateLLM(out);
  };
  const addProvider = () => {
    let n = llm.providers.length + 1;
    let id = `provider-${n}`;
    while (llm.providers.some((p) => p.id === id)) id = `provider-${++n}`;
    updateLLM({
      providers: [
        ...llm.providers,
        {
          id, type: "openai", apiKey: "", baseUrl: "",
          region: "", cacheTTL: "",
        },
      ],
    });
    setExpanded("p:" + llm.providers.length); // 新条目的索引
  };
  const removeProvider = (idx: number) => {
    setExpanded((cur) => (cur === "p:" + idx ? null : cur));
    updateLLM({ providers: llm.providers.filter((_, i) => i !== idx) });
  };

  // --- 模型条目增删改 ---
  const patchModel = (idx: number, patch: Partial<ModelConfig>) => {
    const models = llm.models.map((m, i) => {
      if (i !== idx) return m;
      const next = { ...m, ...patch };
      return { ...next, entryId: genModelId(next.provider, next.modelId) };
    });
    const out: Partial<LLMConfig> = { models };
    // 条目 ID 自动重算后若变化，激活引用同步跟随
    const oldId = llm.models[idx]?.entryId;
    const newId = models[idx]?.entryId;
    if (oldId && newId !== oldId && llm.active === oldId) {
      out.active = newId;
    }
    updateLLM(out);
  };
  const addModel = () => {
    const provider = llm.providers[0]?.id ?? "";
    const m: ModelConfig = {
      entryId: "",
      provider,
      modelId: "",
      contextWindow: 0,
      inputPricePerMillion: 0,
      outputPricePerMillion: 0,
      maxTokens: 0,
      thinkingBudget: null,
      enableThinking: null,
      // 能力声明默认值与后端对齐：工具默认开、图片/推理默认关
      supportsReasoning: false,
      supportsImages: false,
      supportsTools: true,
      reasoningEffort: "",
      reasoningSummary: "",
      temperature: null,
    };
    updateLLM({ models: [...llm.models, m] });
    setExpanded("m:" + llm.models.length); // 新条目的索引
  };
  const removeModel = (idx: number) => {
    setExpanded((cur) => (cur === "m:" + idx ? null : cur));
    updateLLM({ models: llm.models.filter((_, i) => i !== idx) });
  };

  return (
    <PageShell
      title={t("settings.model.title")}
      desc={t("settings.model.desc")}
    >
      <Section title={t("settings.model.current")}>
        <Field label={t("settings.model.defaultModel")} hint={t("settings.model.defaultModelHint")}>
          <select
            className="settings-select"
            value={llm.active}
            onChange={(e) => updateLLM({ active: e.target.value })}
          >
            {llm.models.map((m) => (
              <option key={m.entryId} value={m.entryId}>
                {m.entryId || t("settings.model.unnamed")}
              </option>
            ))}
            {llm.models.length === 0 && <option value="">{t("settings.model.noModels")}</option>}
          </select>
        </Field>
      </Section>

      <Section title={t("settings.model.listTitle", { count: llm.models.length })}>
        {llm.models.map((m, idx) => {
          const key = "m:" + idx;
          const isOpen = expanded === key;
          const pType = providerTypeOf(m.provider);
          return (
            <div className="settings-item" key={key}>
              <div className="settings-item-head" onClick={() => toggle(key)}>
                <span
                  className={`settings-item-star${llm.active === m.entryId && m.entryId ? " on" : ""}`}
                  title={llm.active === m.entryId && m.entryId ? t("settings.model.currentUse") : t("settings.model.setCurrent")}
                  onClick={(e) => {
                    e.stopPropagation();
                    if (m.entryId) updateLLM({ active: m.entryId });
                  }}
                >
                  {llm.active === m.entryId && m.entryId ? "★" : "☆"}
                </span>
                <span className="settings-item-name">
                  {m.entryId || t("settings.model.newEntry")}
                </span>
                <span className="settings-item-sub">
                  {m.provider} · {m.modelId}
                </span>
                <button
                  className="settings-item-del"
                  title={t("settings.model.deleteEntry")}
                  onClick={(e) => {
                    e.stopPropagation();
                    removeModel(idx);
                  }}
                >
                  <X size={13} />
                </button>
              </div>
              {isOpen && (
                <div className="settings-item-body">
                  {/* 第一块：模型身份 */}
                  <div className="settings-group-title">{t("settings.model.identity")}</div>
                  <Field label={t("settings.model.provider")}>
                    <select
                      className="settings-select"
                      value={m.provider}
                      onChange={(e) => patchModel(idx, { provider: e.target.value })}
                    >
                      {llm.providers.map((p) => (
                        <option key={p.id} value={p.id}>
                          {p.id}（{p.type}）
                        </option>
                      ))}
                      {llm.providers.length === 0 && (
                        <option value="">{t("settings.model.addProviderFirst")}</option>
                      )}
                    </select>
                  </Field>
                  <Field
                    label={t("settings.model.modelId")}
                    hint={
                      pType === "ark"
                        ? t("settings.model.modelIdHintArk")
                        : t("settings.model.modelIdHint")
                    }
                  >
                    <input
                      className="settings-input"
                      value={m.modelId}
                      placeholder={t("settings.model.modelIdPlaceholder")}
                      onChange={(e) => patchModel(idx, { modelId: e.target.value })}
                    />
                  </Field>
                  <Field
                    label={t("settings.model.entryId")}
                    hint={t("settings.model.entryIdHint")}
                  >
                    <span className="settings-readonly">
                      {m.entryId || t("settings.model.entryIdAuto")}
                    </span>
                  </Field>

                  {/* 第二块：上下文能力 */}
                  <div className="settings-group-title">{t("settings.model.context")}</div>
                  <Field label={t("settings.model.maxInput")} hint={t("settings.model.maxInputHint")}>
                    <input
                      className="settings-input small"
                      type="number"
                      min={0}
                      placeholder="0"
                      value={m.contextWindow || ""}
                      onChange={(e) =>
                        patchModel(idx, {
                          contextWindow: e.target.valueAsNumber || 0,
                        })
                      }
                    />
                  </Field>
                  <Field
                    label={t("settings.model.maxOutput")}
                    hint={
                      pType === "claude"
                        ? t("settings.model.maxOutputHintClaude")
                        : pType === "deepseek"
                          ? t("settings.model.maxOutputHintDeepseek")
                          : t("settings.model.maxOutputHint")
                    }
                  >
                    <input
                      className="settings-input small"
                      type="number"
                      min={0}
                      placeholder="0"
                      value={m.maxTokens || ""}
                      onChange={(e) =>
                        patchModel(idx, { maxTokens: e.target.valueAsNumber || 0 })
                      }
                    />
                  </Field>
                  <Field label={t("settings.model.inputPrice")} hint={t("settings.model.inputPriceHint")}>
                    <input
                      className="settings-input small"
                      type="number"
                      min={0}
                      step={0.001}
                      placeholder="0"
                      value={m.inputPricePerMillion || ""}
                      onChange={(e) =>
                        patchModel(idx, {
                          inputPricePerMillion: e.target.valueAsNumber || 0,
                        })
                      }
                    />
                  </Field>
                  <Field label={t("settings.model.outputPrice")} hint={t("settings.model.outputPriceHint")}>
                    <input
                      className="settings-input small"
                      type="number"
                      min={0}
                      step={0.001}
                      placeholder="0"
                      value={m.outputPricePerMillion || ""}
                      onChange={(e) =>
                        patchModel(idx, {
                          outputPricePerMillion: e.target.valueAsNumber || 0,
                        })
                      }
                    />
                  </Field>

                  {/* 第三块：能力 */}
                  <div className="settings-group-title">{t("settings.model.capabilities")}</div>
                  <Field
                    label={t("settings.model.capReasoning")}
                    hint={t("settings.model.capReasoningHint")}
                  >
                    <BoolSwitch
                      value={m.supportsReasoning}
                      onChange={(v) => patchModel(idx, { supportsReasoning: v })}
                    />
                  </Field>
                  <Field
                    label={t("settings.model.capImage")}
                    hint={t("settings.model.capImageHint")}
                  >
                    <BoolSwitch
                      value={m.supportsImages}
                      onChange={(v) => patchModel(idx, { supportsImages: v })}
                    />
                  </Field>
                  <Field
                    label={t("settings.model.capTools")}
                    hint={t("settings.model.capToolsHint")}
                  >
                    <BoolSwitch
                      value={m.supportsTools}
                      onChange={(v) => patchModel(idx, { supportsTools: v })}
                    />
                  </Field>

                  {/* 第四块：Reasoning 配置（声明推理能力或供应商有私有推理控件时显示） */}
                  {(m.supportsReasoning ||
                    pType === "gemini" ||
                    THINKING_SWITCH_TYPES.includes(pType)) && (
                    <>
                      <div className="settings-group-title">{t("settings.model.reasoning")}</div>
                      {m.supportsReasoning && (
                        <>
                          <Field
                            label={t("settings.model.effort")}
                            hint={t("settings.model.effortHint")}
                          >
                            <select
                              className="settings-select"
                              value={m.reasoningEffort}
                              onChange={(e) =>
                                patchModel(idx, { reasoningEffort: e.target.value })
                              }
                            >
                              <option value="">{t("settings.model.notSend")}</option>
                              <option value="minimal">minimal</option>
                              <option value="low">low</option>
                              <option value="medium">medium</option>
                              <option value="high">high</option>
                              <option value="xhigh">xhigh</option>
                            </select>
                          </Field>
                          <Field
                            label={t("settings.model.summary")}
                            hint={t("settings.model.summaryHint")}
                          >
                            <select
                              className="settings-select"
                              value={m.reasoningSummary}
                              onChange={(e) =>
                                patchModel(idx, { reasoningSummary: e.target.value })
                              }
                            >
                              <option value="">{t("settings.model.notSend")}</option>
                              <option value="auto">auto</option>
                              <option value="concise">concise</option>
                              <option value="detailed">detailed</option>
                            </select>
                          </Field>
                        </>
                      )}
                      {pType === "gemini" && (
                        <Field
                          label={t("settings.model.budget")}
                          hint={t("settings.model.budgetHint")}
                        >
                          <ThinkingBudgetSeg
                            value={m.thinkingBudget}
                            onChange={(v) => patchModel(idx, { thinkingBudget: v })}
                          />
                        </Field>
                      )}
                      {THINKING_SWITCH_TYPES.includes(pType) && (
                        <Field
                          label={t("settings.model.thinking")}
                          hint={t("settings.model.thinkingHint")}
                        >
                          <Seg
                            value={
                              m.enableThinking === true
                                ? "on"
                                : m.enableThinking === false
                                  ? "off"
                                  : "default"
                            }
                            options={[
                              { value: "default", label: t("settings.model.optDefault") },
                              { value: "on", label: t("settings.model.optOn") },
                              { value: "off", label: t("settings.model.optOff") },
                            ]}
                            onChange={(v) =>
                              patchModel(idx, {
                                enableThinking: v === "default" ? null : v === "on",
                              })
                            }
                          />
                        </Field>
                      )}
                    </>
                  )}

                  {/* 第五块：请求默认值 */}
                  <div className="settings-group-title">{t("settings.model.defaults")}</div>
                  <Field
                    label="Temperature"
                    hint={t("settings.model.temperatureHint")}
                  >
                    <input
                      className="settings-input small"
                      type="number"
                      min={0}
                      max={2}
                      step={0.1}
                      placeholder={t("settings.model.defaultPlaceholder")}
                      value={m.temperature ?? ""}
                      onChange={(e) => {
                        const v = e.target.valueAsNumber;
                        patchModel(idx, { temperature: Number.isNaN(v) ? null : v });
                      }}
                    />
                  </Field>
                </div>
              )}
            </div>
          );
        })}
        <button className="settings-add-btn" onClick={addModel}>
          {t("settings.model.addModel")}
        </button>
      </Section>

      <Section title={t("settings.model.providers", { count: llm.providers.length })}>
        {llm.providers.map((p, idx) => {
          const key = "p:" + idx;
          const isOpen = expanded === key;
          return (
            <div className="settings-item" key={key}>
              <div className="settings-item-head" onClick={() => toggle(key)}>
                <span className="settings-item-name">{p.id}</span>
                <span className="settings-item-sub">
                  {pLabel(p.type)}
                  {providerMeta(p.type).needApiKey
                    ? p.apiKey
                      ? t("settings.model.keyConfigured")
                      : t("settings.model.keyMissing")
                    : ""}
                </span>
                <button
                  className="settings-item-del"
                  title={t("settings.model.deleteProvider")}
                  onClick={(e) => {
                    e.stopPropagation();
                    removeProvider(idx);
                  }}
                >
                  <X size={13} />
                </button>
              </div>
              {isOpen && (
                <div className="settings-item-body">
                  <Field label={t("settings.model.providerId")} hint={t("settings.model.providerIdHint")}>
                    <input
                      className="settings-input"
                      value={p.id}
                      onChange={(e) => patchProvider(idx, { id: e.target.value })}
                    />
                  </Field>
                  <Field
                    label={t("settings.model.type")}
                    hint={t("settings.model.typeHint")}
                  >
                    <select
                      className="settings-select"
                      value={p.type}
                      onChange={(e) => patchProvider(idx, { type: e.target.value })}
                    >
                      {PROVIDER_TYPES.map((pt) => (
                        <option key={pt.value} value={pt.value}>
                          {pLabel(pt.value)}
                        </option>
                      ))}
                    </select>
                  </Field>
                  {providerMeta(p.type).needApiKey && (
                    <Field
                      label="API Key"
                      hint={p.apiKey ? t("settings.model.apiKeyHintSet") : t("settings.model.apiKeyHintUnset")}
                    >
                      <SecretInput
                        value={p.apiKey}
                        placeholder={t("settings.model.apiKeyPlaceholder")}
                        onChange={(v) => patchProvider(idx, { apiKey: v })}
                      />
                    </Field>
                  )}
                  <Field label="Base URL" hint={pHint(p.type)}>
                    <input
                      className="settings-input"
                      value={p.baseUrl}
                      onChange={(e) => patchProvider(idx, { baseUrl: e.target.value })}
                    />
                  </Field>
                  {providerMeta(p.type).hasRegion && (
                    <Field label={t("settings.model.region")} hint={t("settings.model.regionHint")}>
                      <input
                        className="settings-input small"
                        value={p.region}
                        placeholder="cn-beijing"
                        onChange={(e) => patchProvider(idx, { region: e.target.value })}
                      />
                    </Field>
                  )}
                  {providerMeta(p.type).hasCacheTTL && (
                    <Field
                      label={t("settings.model.cacheTTL")}
                      hint={t("settings.model.cacheTTLHint")}
                    >
                      <Seg
                        value={p.cacheTTL === "5m" ? "5m" : p.cacheTTL === "1h" ? "1h" : "off"}
                        options={[
                          { value: "off", label: t("settings.model.optOff") },
                          { value: "5m", label: t("settings.model.ttl5m") },
                          { value: "1h", label: t("settings.model.ttl1h") },
                        ]}
                        onChange={(v) =>
                          patchProvider(idx, { cacheTTL: v === "off" ? "" : v })
                        }
                      />
                    </Field>
                  )}
                </div>
              )}
            </div>
          );
        })}
        <button className="settings-add-btn" onClick={addProvider}>
          {t("settings.model.addProvider")}
        </button>
      </Section>
    </PageShell>
  );
}

/** 思考预算分段控件（gemini 模型条目用）。值语义：null 默认 / -1 动态 / 0 关闭 / >0 固定预算 */
function ThinkingBudgetSeg({
  value,
  onChange,
}: {
  value: number | null;
  onChange: (v: number | null) => void;
}) {
  const { t } = useTranslation();
  const mode =
    value === null
      ? "default"
      : value === -1
        ? "dynamic"
        : value === 0
          ? "off"
          : "custom";
  return (
    <>
      <Seg
        value={mode}
        options={[
          { value: "default", label: t("settings.model.optDefault") },
          { value: "dynamic", label: t("settings.model.optDynamic") },
          { value: "off", label: t("settings.model.optOff") },
          { value: "custom", label: t("settings.model.optCustom") },
        ]}
        onChange={(m) =>
          onChange(
            m === "default" ? null : m === "dynamic" ? -1 : m === "off" ? 0 : 1024,
          )
        }
      />
      {mode === "custom" && (
        <input
          className="settings-input small"
          type="number"
          min={1}
          value={value ?? 1024}
          onChange={(e) => onChange(Math.max(1, e.target.valueAsNumber || 1))}
        />
      )}
    </>
  );
}

function AgentPage({
  draft,
  update,
}: {
  draft: AppConfig;
  update: (fn: (d: AppConfig) => AppConfig) => void;
}) {
  const { t } = useTranslation();
  return (
    <PageShell title={t("settings.agent.title")} desc={t("settings.agent.desc")}>
      <Section title={t("settings.agent.execution")}>
        <Field
          label={t("settings.agent.maxIter")}
          hint={t("settings.agent.maxIterHint")}
        >
          <input
            className="settings-input small"
            type="number"
            min={1}
            value={draft.agent.maxIterations}
            onChange={(e) =>
              update((d) => ({
                ...d,
                agent: {
                  ...d.agent,
                  maxIterations: Math.max(1, e.target.valueAsNumber || 1),
                },
              }))
            }
          />
        </Field>
        <Field
          label={t("settings.agent.iterTimeout")}
          hint={t("settings.agent.iterTimeoutHint")}
        >
          <input
            className="settings-input small"
            type="number"
            min={10}
            step={10}
            // 线缆格式是纳秒（Go time.Duration），UI 以秒呈现
            value={Math.round(draft.agent.iterationTimeout / 1e9) || ""}
            onChange={(e) =>
              update((d) => ({
                ...d,
                agent: {
                  ...d.agent,
                  iterationTimeout: Math.max(10, e.target.valueAsNumber || 10) * 1e9,
                },
              }))
            }
          />
        </Field>
        <Field
          label={t("settings.agent.compressThreshold")}
          hint={t("settings.agent.compressThresholdHint")}
        >
          <input
            className="settings-slider"
            type="range"
            min={0.5}
            max={0.95}
            step={0.05}
            value={draft.agent.compressionThreshold}
            onChange={(e) =>
              update((d) => ({
                ...d,
                agent: {
                  ...d.agent,
                  compressionThreshold: Number(e.target.value),
                },
              }))
            }
          />
          <span className="settings-slider-value">
            {(draft.agent.compressionThreshold * 100).toFixed(0)}%
          </span>
        </Field>
        <Field
          label={t("settings.agent.keepRounds")}
          hint={t("settings.agent.keepRoundsHint")}
        >
          <input
            className="settings-input small"
            type="number"
            min={1}
            value={draft.agent.compressionKeepTurns}
            onChange={(e) =>
              update((d) => ({
                ...d,
                agent: {
                  ...d.agent,
                  compressionKeepTurns: Math.max(1, e.target.valueAsNumber || 1),
                },
              }))
            }
          />
        </Field>
        <Field
          label={t("settings.agent.minBatch")}
          hint={t("settings.agent.minBatchHint")}
        >
          <input
            className="settings-input small"
            type="number"
            min={1}
            value={draft.agent.compressionMinBatch}
            onChange={(e) =>
              update((d) => ({
                ...d,
                agent: {
                  ...d.agent,
                  compressionMinBatch: Math.max(1, e.target.valueAsNumber || 1),
                },
              }))
            }
          />
        </Field>
        <Field
          label={t("settings.agent.circuitThreshold")}
          hint={t("settings.agent.circuitThresholdHint")}
        >
          <input
            className="settings-input small"
            type="number"
            min={1}
            value={draft.agent.compressionMaxFailures}
            onChange={(e) =>
              update((d) => ({
                ...d,
                agent: {
                  ...d.agent,
                  compressionMaxFailures: Math.max(1, e.target.valueAsNumber || 1),
                },
              }))
            }
          />
        </Field>
      </Section>
    </PageShell>
  );
}

function TracePage({
  draft,
  update,
}: {
  draft: AppConfig;
  update: (fn: (d: AppConfig) => AppConfig) => void;
}) {
  const { t } = useTranslation();
  return (
    <PageShell
      title={t("settings.trace.title")}
      desc={t("settings.trace.desc")}
    >
      <Section title={t("settings.trace.masterSwitch")}>
        <Field
          label={t("settings.trace.enable")}
          hint={t("settings.trace.enableHint")}
        >
          <button
            className={`switch${draft.trace.enabled ? " on" : ""}`}
            role="switch"
            aria-checked={draft.trace.enabled}
            onClick={() =>
              update((d) => ({
                ...d,
                trace: { ...d.trace, enabled: !d.trace.enabled },
              }))
            }
          >
            <span className="switch-thumb" />
          </button>
        </Field>
      </Section>
      <Section title={t("settings.trace.otlp")}>
        <Field
          label={t("settings.trace.httpEndpoint")}
          hint={t("settings.trace.httpHint")}
        >
          <input
            className="settings-input"
            value={draft.trace.otlpHttpEndpoint}
            placeholder="localhost:4318"
            onChange={(e) =>
              update((d) => ({
                ...d,
                trace: { ...d.trace, otlpHttpEndpoint: e.target.value },
              }))
            }
          />
        </Field>
        <Field
          label={t("settings.trace.grpcEndpoint")}
          hint={t("settings.trace.grpcHint")}
        >
          <input
            className="settings-input"
            value={draft.trace.otlpGrpcEndpoint}
            placeholder="localhost:4317"
            onChange={(e) =>
              update((d) => ({
                ...d,
                trace: { ...d.trace, otlpGrpcEndpoint: e.target.value },
              }))
            }
          />
        </Field>
      </Section>
    </PageShell>
  );
}

function AboutPage() {
  const { t } = useTranslation();
  return (
    <PageShell title={t("settings.about.title")} desc={t("settings.about.desc")}>
      <div className="settings-about">
        <div className="settings-about-name">TARS</div>
        <div className="settings-about-version">v0.0.1</div>
        <p className="settings-about-desc">
          {t("settings.about.tagline")}
        </p>
      </div>
    </PageShell>
  );
}
