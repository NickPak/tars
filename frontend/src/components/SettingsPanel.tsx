import { useEffect, useState } from "react";
import type { ReactNode } from "react";
import {
  Activity,
  Bot,
  Brain,
  Check,
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
import type { AppConfigView, LLMConfigView, ModelView, ProviderView } from "../types";
import { ConfirmDialog } from "./Dialog";

interface NavItem {
  tab: SettingsTab;
  label: string;
  icon: ReactNode;
  /** 规划中功能的占位页签 */
  planned?: boolean;
}

const NAV_ITEMS: NavItem[] = [
  { tab: "general", label: "通用", icon: <SlidersHorizontal size={15} /> },
  { tab: "model", label: "模型", icon: <Brain size={15} /> },
  { tab: "agent", label: "Agent", icon: <Bot size={15} /> },
  { tab: "trace", label: "追踪", icon: <Activity size={15} /> },
  { tab: "skills", label: "技能", icon: <Sparkles size={15} />, planned: true },
  { tab: "mcp", label: "MCP 与工具", icon: <Plug size={15} />, planned: true },
  { tab: "appearance", label: "外观", icon: <Palette size={15} />, planned: true },
  { tab: "about", label: "关于", icon: <Info size={15} /> },
];

/** 有真实配置读写能力的页签（其余为占位） */
const REAL_TABS: SettingsTab[] = ["general", "model", "agent", "trace"];

function errText(e: unknown): string {
  return e instanceof Error ? e.message : String(e);
}

/**
 * 设置面板：居中模态（左侧分类导航 + 右侧内容区），参考 DeepSeek-Reasonix。
 * 数据流：打开时 GetAppConfig → 本地 draft 编辑 → 保存时 SaveAppConfig
 * 全量提交（后端按键级合并写回 config.yaml，保留注释与 apiKey 引用）。
 */
export default function SettingsPanel() {
  const open = useSettingsStore((s) => s.open);
  const tab = useSettingsStore((s) => s.tab);
  const setTab = useSettingsStore((s) => s.setTab);
  const closeSettings = useSettingsStore((s) => s.closeSettings);

  const [draft, setDraft] = useState<AppConfigView | null>(null);
  const [baseline, setBaseline] = useState<AppConfigView | null>(null);
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

  const update = (fn: (d: AppConfigView) => AppConfigView) => {
    setDraft((d) => (d ? fn(d) : d));
    setSaved(false);
  };
  const updateLLM = (patch: Partial<LLMConfigView>) =>
    update((d) => ({ ...d, llm: { ...d.llm, ...patch } }));

  const handleSave = async () => {
    if (!draft || saving) return;
    setSaving(true);
    setError(null);
    try {
      await agentApi.saveAppConfig(draft);
      // 重新拉取（apiKey 被消费后回到脱敏状态）
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
          <span className="settings-title">设置</span>
          <button
            className="settings-close"
            title="关闭"
            aria-label="关闭设置"
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
                <span>{item.label}</span>
                {item.planned && (
                  <span className="settings-nav-badge">规划中</span>
                )}
              </button>
            ))}
          </nav>

          <main className="settings-content">
            {loading && <div className="settings-loading">加载中…</div>}
            {!loading && error && !draft && (
              <div className="settings-load-error">加载配置失败：{error}</div>
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
                {tab === "skills" && (
                  <PlaceholderPage
                    icon={<Sparkles size={28} />}
                    title="技能（Skills）"
                    desc="领域知识与专项操作流程的能力层。元数据常驻上下文，完整内容按需加载。"
                    items={[
                      "SKILL.md 文档规范（路由式描述 + 反例）",
                      "load_skill 工具：按需加载，幂等不重复注入",
                      "扁平存储 + 自动生成的索引",
                    ]}
                  />
                )}
                {tab === "mcp" && (
                  <PlaceholderPage
                    icon={<Plug size={28} />}
                    title="MCP 与工具"
                    desc="通过 MCP 协议接入外部系统（日历、数据库、第三方 API）。"
                    items={[
                      "discover_tools 工具：语义检索可用工具",
                      "服务器级索引常驻，schema 按需加载",
                      "进程延迟启动 + 安全审查清单",
                    ]}
                  />
                )}
                {tab === "appearance" && (
                  <PlaceholderPage
                    icon={<Palette size={28} />}
                    title="外观"
                    desc="主题与排版定制。"
                    items={[
                      "浅色 / 深色 / 跟随系统主题",
                      "界面与会话字体、字号定制",
                      "代码块与元数据排版",
                    ]}
                  />
                )}
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
                已保存（工作目录修改需重启生效）
              </span>
            )}
            <span className="settings-foot-spacer" />
            <button
              className="dialog-btn secondary"
              disabled={!dirty || saving}
              onClick={() => setDraft(baseline)}
            >
              放弃更改
            </button>
            <button
              className="dialog-btn primary"
              disabled={!dirty || saving}
              onClick={() => void handleSave()}
            >
              {saving ? "保存中…" : "保存"}
            </button>
          </div>
        )}
      </div>

      <ConfirmDialog
        open={discardConfirm}
        message="有未保存的修改，确定放弃并关闭？"
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

function GeneralPage({
  draft,
  update,
}: {
  draft: AppConfigView;
  update: (fn: (d: AppConfigView) => AppConfigView) => void;
}) {
  const handleBrowse = async () => {
    const dir = await agentApi.openDirectoryDialog();
    if (dir) update((d) => ({ ...d, workDir: dir }));
  };

  return (
    <PageShell title="通用" desc="应用级基础设置。">
      <Section title="存储">
        <Field
          label="工作目录"
          hint="Agent 工具操作与会话数据的根目录，留空使用默认目录（~/tars）。修改后需重启生效。"
        >
          <input
            className="settings-input"
            value={draft.workDir}
            placeholder="默认（~/tars）"
            onChange={(e) =>
              update((d) => ({ ...d, workDir: e.target.value }))
            }
          />
          <button
            className="dialog-btn secondary"
            title="浏览目录"
            onClick={() => void handleBrowse()}
          >
            <FolderOpen size={14} />
          </button>
        </Field>
      </Section>
      <Section title="语言">
        <Field label="界面语言" hint="多语言界面规划中，当前仅支持中文。">
          <Seg
            value="zh"
            options={[{ value: "zh", label: "中文" }]}
            onChange={() => {}}
            disabled
          />
        </Field>
      </Section>
    </PageShell>
  );
}

/** 供应商类型元信息：UI 标签、各字段的显隐与提示（对应后端原生组件能力） */
const PROVIDER_TYPES: {
  value: string;
  label: string;
  needApiKey: boolean;
  needAkSk: boolean;
  needBaseUrl: boolean;
  baseUrlHint: string;
  hasRegion: boolean;
  hasCacheTTL: boolean;
}[] = [
  {
    value: "gemini", label: "Gemini", needApiKey: true, needAkSk: false,
    needBaseUrl: false, baseUrlHint: "", hasRegion: false, hasCacheTTL: false,
  },
  {
    value: "openai", label: "OpenAI 兼容", needApiKey: true, needAkSk: false,
    needBaseUrl: true,
    baseUrlHint: "必填。覆盖 OpenAI 官方与所有兼容端点（Moonshot/OpenRouter/本地 vLLM 等），如 https://api.openai.com/v1",
    hasRegion: false, hasCacheTTL: false,
  },
  {
    value: "claude", label: "Claude", needApiKey: true, needAkSk: false,
    needBaseUrl: false, baseUrlHint: "可选，自定义 Anthropic 端点",
    hasRegion: false, hasCacheTTL: true,
  },
  {
    value: "deepseek", label: "DeepSeek", needApiKey: true, needAkSk: false,
    needBaseUrl: false, baseUrlHint: "可选，默认官方端点",
    hasRegion: false, hasCacheTTL: false,
  },
  {
    value: "qwen", label: "Qwen（百炼）", needApiKey: true, needAkSk: false,
    needBaseUrl: true,
    baseUrlHint: "必填，如 https://dashscope.aliyuncs.com/compatible-mode/v1",
    hasRegion: false, hasCacheTTL: false,
  },
  {
    value: "ark", label: "火山方舟 ARK", needApiKey: true, needAkSk: false,
    needBaseUrl: false, baseUrlHint: "可选，默认官方端点",
    hasRegion: true, hasCacheTTL: false,
  },
  {
    value: "ollama", label: "Ollama（本地）", needApiKey: false, needAkSk: false,
    needBaseUrl: false, baseUrlHint: "可选，默认 http://localhost:11434",
    hasRegion: false, hasCacheTTL: false,
  },
  {
    value: "qianfan", label: "百度千帆", needApiKey: false, needAkSk: true,
    needBaseUrl: false, baseUrlHint: "", hasRegion: false, hasCacheTTL: false,
  },
];

function providerMeta(type: string) {
  return PROVIDER_TYPES.find((t) => t.value === type) ?? PROVIDER_TYPES[1];
}

/** 哪些供应商类型的模型条目显示"思考模式"开关 */
const THINKING_SWITCH_TYPES = ["deepseek", "qwen", "ark", "ollama"];

/** 各供应商类型的内置 reasoning 回放策略（与后端 defaultReasoningPolicies 对应） */
const REASONING_POLICY_DEFAULTS: Record<string, string> = {
  gemini: "replay",
  deepseek: "strip",
  qwen: "strip",
  ark: "strip",
  openai: "keep",
  claude: "keep",
  ollama: "keep",
  qianfan: "keep",
};

const REASONING_POLICY_LABELS: Record<string, string> = {
  replay: "回放",
  strip: "剥离",
  keep: "透传",
};

/** 模型页：供应商列表 + 模型列表（多供应商管理） */
function ModelPage({
  draft,
  updateLLM,
}: {
  draft: AppConfigView;
  updateLLM: (patch: Partial<LLMConfigView>) => void;
}) {
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

  const patchProvider = (idx: number, patch: Partial<ProviderView>) => {
    const providers = llm.providers.map((p, i) =>
      i === idx ? { ...p, ...patch } : p,
    );
    const out: Partial<LLMConfigView> = { providers };
    // 供应商 ID 变更时级联：引用它的模型条目跟随改 provider 并重新生成条目 ID
    const oldId = llm.providers[idx]?.id;
    const newId = providers[idx]?.id;
    if (patch.id !== undefined && oldId && newId !== oldId) {
      const models = llm.models.map((m) =>
        m.provider !== oldId
          ? m
          : { ...m, provider: newId, id: genModelId(newId, m.modelId) },
      );
      out.models = models;
      // 激活条目引用同步
      const activeIdx = llm.models.findIndex((m) => m.id === llm.active);
      if (activeIdx >= 0) out.active = models[activeIdx].id;
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
          id, type: "openai", apiKey: "", apiKeySet: false, baseUrl: "",
          timeout: "", accessKey: "", secretKey: "", keySet: false,
          region: "", cacheTTL: "", reasoningPolicy: "",
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
  const patchModel = (idx: number, patch: Partial<ModelView>) => {
    const models = llm.models.map((m, i) => {
      if (i !== idx) return m;
      const next = { ...m, ...patch };
      return { ...next, id: genModelId(next.provider, next.modelId) };
    });
    const out: Partial<LLMConfigView> = { models };
    // 条目 ID 自动重算后若变化，激活引用同步跟随
    const oldId = llm.models[idx]?.id;
    const newId = models[idx]?.id;
    if (oldId && newId !== oldId && llm.active === oldId) {
      out.active = newId;
    }
    updateLLM(out);
  };
  const addModel = () => {
    const provider = llm.providers[0]?.id ?? "";
    let m: ModelView = {
      id: "",
      provider,
      modelId: "",
      contextWindow: 0,
      inputPricePerMillion: 0,
      outputPricePerMillion: 0,
      maxTokens: 0,
      thinkingBudget: "",
      enableThinking: "",
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
      title="模型"
      desc="供应商（鉴权与端点）+ 模型条目（模型 ID 与计量参数），保存后立即生效。"
    >
      <Section title="当前使用">
        <Field label="默认模型" hint="新对话与未切换会话使用的模型条目。">
          <select
            className="settings-select"
            value={llm.active}
            onChange={(e) => updateLLM({ active: e.target.value })}
          >
            {llm.models.map((m) => (
              <option key={m.id} value={m.id}>
                {m.id || "（未命名条目）"}
              </option>
            ))}
            {llm.models.length === 0 && <option value="">（无可用模型）</option>}
          </select>
        </Field>
      </Section>

      <Section title={`模型列表（${llm.models.length}）`}>
        {llm.models.map((m, idx) => {
          const key = "m:" + idx;
          const isOpen = expanded === key;
          const pType = providerTypeOf(m.provider);
          return (
            <div className="settings-item" key={key}>
              <div className="settings-item-head" onClick={() => toggle(key)}>
                <span
                  className={`settings-item-star${llm.active === m.id && m.id ? " on" : ""}`}
                  title={llm.active === m.id && m.id ? "当前使用" : "设为当前使用"}
                  onClick={(e) => {
                    e.stopPropagation();
                    if (m.id) updateLLM({ active: m.id });
                  }}
                >
                  {llm.active === m.id && m.id ? "★" : "☆"}
                </span>
                <span className="settings-item-name">
                  {m.id || "（新条目）"}
                </span>
                <span className="settings-item-sub">
                  {m.provider} · {m.modelId}
                </span>
                <button
                  className="settings-item-del"
                  title="删除该模型条目"
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
                  <Field label="供应商">
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
                        <option value="">（请先添加供应商）</option>
                      )}
                    </select>
                  </Field>
                  <Field
                    label="模型 ID"
                    hint={
                      pType === "ark"
                        ? "火山方舟填推理接入点 endpoint ID（ep-xxx），不是模型名。"
                        : "发送给 API 的真实模型名。"
                    }
                  >
                    <input
                      className="settings-input"
                      value={m.modelId}
                      placeholder="如 deepseek-chat"
                      onChange={(e) => patchModel(idx, { modelId: e.target.value })}
                    />
                  </Field>
                  <Field
                    label="条目 ID"
                    hint="唯一标识，由 供应商/模型ID 自动生成。"
                  >
                    <span className="settings-readonly">
                      {m.id || "（输入模型 ID 后自动生成）"}
                    </span>
                  </Field>
                  <Field
                    label="最大输出 tokens"
                    hint={
                      pType === "claude"
                        ? "Claude 必填（Anthropic API 强制要求）。"
                        : pType === "deepseek"
                          ? "DeepSeek 默认 4096，上限 8192。0 = 用默认。"
                          : "0 = 不设置（用服务端默认）。"
                    }
                  >
                    <input
                      className="settings-input small"
                      type="number"
                      min={0}
                      value={m.maxTokens}
                      onChange={(e) =>
                        patchModel(idx, { maxTokens: e.target.valueAsNumber || 0 })
                      }
                    />
                  </Field>
                  <Field label="上下文窗口" hint="tokens 数，0 = 未知。">
                    <input
                      className="settings-input small"
                      type="number"
                      min={0}
                      value={m.contextWindow}
                      onChange={(e) =>
                        patchModel(idx, {
                          contextWindow: e.target.valueAsNumber || 0,
                        })
                      }
                    />
                  </Field>
                  <Field label="输入价格" hint="元 / 百万 tokens（0 不展示费用）。">
                    <input
                      className="settings-input small"
                      type="number"
                      min={0}
                      step={0.001}
                      value={m.inputPricePerMillion}
                      onChange={(e) =>
                        patchModel(idx, {
                          inputPricePerMillion: e.target.valueAsNumber || 0,
                        })
                      }
                    />
                  </Field>
                  <Field label="输出价格" hint="元 / 百万 tokens。">
                    <input
                      className="settings-input small"
                      type="number"
                      min={0}
                      step={0.001}
                      value={m.outputPricePerMillion}
                      onChange={(e) =>
                        patchModel(idx, {
                          outputPricePerMillion: e.target.valueAsNumber || 0,
                        })
                      }
                    />
                  </Field>
                  {pType === "gemini" && (
                    <Field
                      label="思考预算"
                      hint="仅 gemini 供应商生效。flash-lite 系列默认关闭，需显式开启动态思考。"
                    >
                      <ThinkingBudgetSeg
                        value={m.thinkingBudget}
                        onChange={(v) => patchModel(idx, { thinkingBudget: v })}
                      />
                    </Field>
                  )}
                  {THINKING_SWITCH_TYPES.includes(pType) && (
                    <Field
                      label="思考模式"
                      hint="映射到供应商原生字段：DeepSeek thinking.type / Qwen enable_thinking / ARK thinking.type / Ollama think。"
                    >
                      <Seg
                        value={
                          m.enableThinking === "on"
                            ? "on"
                            : m.enableThinking === "off"
                              ? "off"
                              : "default"
                        }
                        options={[
                          { value: "default", label: "默认" },
                          { value: "on", label: "开启" },
                          { value: "off", label: "关闭" },
                        ]}
                        onChange={(v) =>
                          patchModel(idx, {
                            enableThinking: v === "default" ? "" : v,
                          })
                        }
                      />
                    </Field>
                  )}
                </div>
              )}
            </div>
          );
        })}
        <button className="settings-add-btn" onClick={addModel}>
          + 添加模型
        </button>
      </Section>

      <Section title={`供应商（${llm.providers.length}）`}>
        {llm.providers.map((p, idx) => {
          const key = "p:" + idx;
          const isOpen = expanded === key;
          return (
            <div className="settings-item" key={key}>
              <div className="settings-item-head" onClick={() => toggle(key)}>
                <span className="settings-item-name">{p.id}</span>
                <span className="settings-item-sub">
                  {providerMeta(p.type).label}
                  {providerMeta(p.type).needAkSk
                    ? p.keySet
                      ? " · 已配置 AK/SK"
                      : " · 未配置 AK/SK"
                    : providerMeta(p.type).needApiKey
                      ? p.apiKeySet
                        ? " · 已配置 Key"
                        : " · 未配置 Key"
                      : ""}
                </span>
                <button
                  className="settings-item-del"
                  title="删除该供应商"
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
                  <Field label="供应商 ID" hint="唯一标识，模型条目通过它引用。">
                    <input
                      className="settings-input"
                      value={p.id}
                      onChange={(e) => patchProvider(idx, { id: e.target.value })}
                    />
                  </Field>
                  <Field
                    label="类型"
                    hint="每种类型对应 eino 原生组件（非 OpenAI 兼容转译），保留模型私有特性。"
                  >
                    <select
                      className="settings-select"
                      value={p.type}
                      onChange={(e) => patchProvider(idx, { type: e.target.value })}
                    >
                      {PROVIDER_TYPES.map((t) => (
                        <option key={t.value} value={t.value}>
                          {t.label}
                        </option>
                      ))}
                    </select>
                  </Field>
                  {providerMeta(p.type).needApiKey && (
                    <Field
                      label="API Key"
                      hint={
                        p.apiKeySet
                          ? "已配置。输入新值以更换；留空保持不变。"
                          : "尚未配置 API Key。"
                      }
                    >
                      <input
                        className="settings-input"
                        type="password"
                        value={p.apiKey}
                        placeholder={p.apiKeySet ? "已配置（未修改）" : "输入 API Key"}
                        autoComplete="off"
                        onChange={(e) => patchProvider(idx, { apiKey: e.target.value })}
                      />
                    </Field>
                  )}
                  {providerMeta(p.type).needAkSk && (
                    <>
                      <Field
                        label="Access Key"
                        hint={p.keySet ? "已配置。输入新值以更换；留空保持不变。" : "千帆平台 AK。"}
                      >
                        <input
                          className="settings-input"
                          type="password"
                          value={p.accessKey}
                          placeholder={p.keySet ? "已配置（未修改）" : "输入 Access Key"}
                          autoComplete="off"
                          onChange={(e) => patchProvider(idx, { accessKey: e.target.value })}
                        />
                      </Field>
                      <Field label="Secret Key" hint="千帆平台 SK。">
                        <input
                          className="settings-input"
                          type="password"
                          value={p.secretKey}
                          placeholder={p.keySet ? "已配置（未修改）" : "输入 Secret Key"}
                          autoComplete="off"
                          onChange={(e) => patchProvider(idx, { secretKey: e.target.value })}
                        />
                      </Field>
                    </>
                  )}
                  <Field label="Base URL" hint={providerMeta(p.type).baseUrlHint}>
                    <input
                      className="settings-input"
                      value={p.baseUrl}
                      onChange={(e) => patchProvider(idx, { baseUrl: e.target.value })}
                    />
                  </Field>
                  {providerMeta(p.type).hasRegion && (
                    <Field label="区域" hint="火山引擎区域，留空默认 cn-beijing。">
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
                      label="自动前缀缓存"
                      hint="在 system、工具定义与每轮最后一条 user 消息上打缓存断点，长会话显著降本。"
                    >
                      <Seg
                        value={p.cacheTTL === "5m" ? "5m" : p.cacheTTL === "1h" ? "1h" : "off"}
                        options={[
                          { value: "off", label: "关闭" },
                          { value: "5m", label: "5 分钟" },
                          { value: "1h", label: "1 小时" },
                        ]}
                        onChange={(v) =>
                          patchProvider(idx, { cacheTTL: v === "off" ? "" : v })
                        }
                      />
                    </Field>
                  )}
                  <Field label="请求超时" hint="如 60s、2m；留空不设超时。">
                    <input
                      className="settings-input small"
                      value={p.timeout}
                      placeholder="60s"
                      onChange={(e) => patchProvider(idx, { timeout: e.target.value })}
                    />
                  </Field>
                  <Field
                    label="思考链回放"
                    hint={`历史消息中 reasoning 的处理策略。这是协议要求而非偏好：选错会导致 400（DeepSeek/Qwen/ARK 必须剥离，Gemini 必须回放）。当前内置默认：${REASONING_POLICY_LABELS[REASONING_POLICY_DEFAULTS[p.type] ?? "keep"]}。`}
                  >
                    <Seg
                      value={p.reasoningPolicy || "default"}
                      options={[
                        {
                          value: "default",
                          label: `默认（${REASONING_POLICY_LABELS[REASONING_POLICY_DEFAULTS[p.type] ?? "keep"]}）`,
                        },
                        { value: "replay", label: "回放" },
                        { value: "strip", label: "剥离" },
                        { value: "keep", label: "透传" },
                      ]}
                      onChange={(v) =>
                        patchProvider(idx, {
                          reasoningPolicy: v === "default" ? "" : v,
                        })
                      }
                    />
                  </Field>
                </div>
              )}
            </div>
          );
        })}
        <button className="settings-add-btn" onClick={addProvider}>
          + 添加供应商
        </button>
      </Section>
    </PageShell>
  );
}

/** 思考预算分段控件（gemini 模型条目用） */
function ThinkingBudgetSeg({
  value,
  onChange,
}: {
  value: string;
  onChange: (v: string) => void;
}) {
  const mode =
    value === ""
      ? "default"
      : value === "-1"
        ? "dynamic"
        : value === "0"
          ? "off"
          : "custom";
  return (
    <>
      <Seg
        value={mode}
        options={[
          { value: "default", label: "默认" },
          { value: "dynamic", label: "动态" },
          { value: "off", label: "关闭" },
          { value: "custom", label: "自定义" },
        ]}
        onChange={(m) =>
          onChange(
            m === "default"
              ? ""
              : m === "dynamic"
                ? "-1"
                : m === "off"
                  ? "0"
                  : "1024",
          )
        }
      />
      {mode === "custom" && (
        <input
          className="settings-input small"
          type="number"
          min={1}
          value={value}
          onChange={(e) => onChange(e.target.value)}
        />
      )}
    </>
  );
}

function AgentPage({
  draft,
  update,
}: {
  draft: AppConfigView;
  update: (fn: (d: AppConfigView) => AppConfigView) => void;
}) {
  return (
    <PageShell title="Agent" desc="ReAct 循环运行时行为，保存后立即生效。">
      <Section title="执行">
        <Field
          label="最大迭代次数"
          hint="LLM→工具→LLM 一轮算一次。防止死循环烧 token；复杂多文件任务需要较大预算。"
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
          label="上下文压缩阈值"
          hint="上下文使用占比超过该值时触发历史压缩（压缩功能规划中，当前仅状态栏展示）。"
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
      </Section>
    </PageShell>
  );
}

function TracePage({
  draft,
  update,
}: {
  draft: AppConfigView;
  update: (fn: (d: AppConfigView) => AppConfigView) => void;
}) {
  return (
    <PageShell
      title="追踪"
      desc="OpenTelemetry 链路追踪，仅导出到配置的 OTLP 收集器（无本地文件落盘）。修改保存后立即生效。"
    >
      <Section title="总开关">
        <Field
          label="启用追踪"
          hint="关闭时即使配置了端点也不产生任何 span。"
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
      <Section title="OTLP 导出">
        <Field
          label="OTLP / HTTP 端点"
          hint="如 localhost:4318（Jaeger）：docker run -d -p 16686:16686 -p 4318:4318 jaegertracing/all-in-one"
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
          label="OTLP / gRPC 端点"
          hint="如 localhost:4317（Arize Phoenix）：docker run -d -p 6006:6006 -p 4317:4317 arizephoenix/phoenix"
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

function PlaceholderPage({
  icon,
  title,
  desc,
  items,
}: {
  icon: ReactNode;
  title: string;
  desc: string;
  items: string[];
}) {
  return (
    <PageShell title={title} desc={desc}>
      <div className="settings-placeholder">
        <div className="settings-placeholder-icon">{icon}</div>
        <ul className="settings-placeholder-list">
          {items.map((it) => (
            <li key={it}>{it}</li>
          ))}
        </ul>
        <span className="settings-placeholder-badge">规划中 · 敬请期待</span>
      </div>
    </PageShell>
  );
}

function AboutPage() {
  return (
    <PageShell title="关于" desc="应用信息。">
      <div className="settings-about">
        <div className="settings-about-name">TARS</div>
        <div className="settings-about-version">v0.0.1</div>
        <p className="settings-about-desc">
          通用 AI Agent 桌面应用 · Wails v3 + Go + React
        </p>
      </div>
    </PageShell>
  );
}
