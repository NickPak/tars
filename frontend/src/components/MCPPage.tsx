/**
 * MCP 与工具页（设置面板 mcp 页签）：管理外部 MCP 工具服务器。
 * 与技能页同生命周期：服务器配置由后端 mcp.Manager 在
 * <workDir>/mcp/servers.yaml 自管读写，本页所有变更（添加/删除/启停）
 * 即改即存、立即生效——不经 AppConfig draft 保存流，页面无 dirty 态。
 * 探测（抓取工具清单缓存）是独立动作，直接对已落盘配置执行。
 *
 * 添加服务器提供三种方式（对齐 Reasonix）：快速安装（粘贴命令或 JSON，
 * 自动识别）、手动配置（逐项表单）、JSON（Claude Desktop 格式整段粘贴）。
 */
import { useEffect, useState } from "react";
import { Plug, RefreshCw, Trash2, Zap } from "lucide-react";
import { agentApi } from "../services/agentApi";
import type { MCPServerConfig, MCPServerInfo } from "../types";
import {
  deriveName,
  envToText,
  parseQuick,
  parseServerJSON,
  textToEnv,
  toServerJSON,
  tokenizeCommand,
  validServerName,
  type ParsedServer,
} from "./mcpServerForm";

function errText(e: unknown): string {
  return e instanceof Error ? e.message : String(e);
}

const SOURCE_TYPES = ["search", "read", "parse", "query"];
const RISK_LEVELS = ["low", "medium", "high"];

const EMPTY_FORM: MCPServerConfig = {
  command: "",
  args: [],
  env: {},
  description: "",
  sourceType: "query",
  enabled: true, // 添加即启用（失败安全由"显式添加"动作本身保证）
  risk: "medium",
};

type EditorMode = "quick" | "form" | "json";

export default function MCPPage() {
  const [infos, setInfos] = useState<MCPServerInfo[] | null>(null);
  const [error, setError] = useState<string | null>(null); // 列表加载错误（块顶部）
  const [notice, setNotice] = useState<string | null>(null); // 添加成功提示（添加按钮上方）
  const [probing, setProbing] = useState<string | null>(null);
  // 行内操作（启停/删除）进行中：禁用全部行按钮防连点
  const [rowBusy, setRowBusy] = useState(false);
  // 行内反馈（显示在对应条目下方，而非块底部）
  const [rowMsg, setRowMsg] = useState<{ name: string; kind: "ok" | "err"; text: string } | null>(null);

  // ---- 添加服务器（三态编辑器）----
  const [mode, setMode] = useState<EditorMode>("quick");
  const [quickText, setQuickText] = useState("");
  const [quickErr, setQuickErr] = useState<string | null>(null);
  const [formName, setFormName] = useState("");
  const [form, setForm] = useState<MCPServerConfig>(EMPTY_FORM);
  const [formErr, setFormErr] = useState<string | null>(null);
  const [envDraft, setEnvDraft] = useState(""); // env 文本（提交时解析，编辑期容忍中间态）
  const [jsonText, setJsonText] = useState("");
  const [jsonErr, setJsonErr] = useState<string | null>(null);
  // 附加字段（quick/JSON 模式共用）：命令行/JSON 文本装不下或不直观的
  // env/描述/类型/风险，在安装时就有补充入口（切入手动配置时并入表单）。
  const [extraEnv, setExtraEnv] = useState("");
  const [extraDesc, setExtraDesc] = useState("");
  const [extraSourceType, setExtraSourceType] = useState(""); // "" = 自动（以解析结果为准）
  const [extraRisk, setExtraRisk] = useState("");

  /** 列表唯一数据源是后端（变更即改即存，读到的就是生效值） */
  const refresh = async () => {
    try {
      setInfos(await agentApi.listMCPServers());
      setError(null);
    } catch (e) {
      setError(errText(e));
    }
  };
  useEffect(() => {
    void refresh();
  }, []);

  /** 表单整体赋值（模式互转/重置共用）：同步 env 文本域 */
  const applyForm = (name: string, cfg: MCPServerConfig) => {
    setFormName(name);
    setForm(cfg);
    setEnvDraft(envToText(cfg.env));
  };

  const clearExtras = () => {
    setExtraEnv("");
    setExtraDesc("");
    setExtraSourceType("");
    setExtraRisk("");
  };

  /** 附加字段合并（quick/JSON 提交、转入手动配置时）：
   *  env 与解析结果按键合并（输入框优先）；描述非空才覆盖；
   *  类型/风险选了具体值才覆盖（"自动" = 以解析结果为准，解析缺省 query/medium）。
   *  env 非法行在此报错（调用方 try 兜底）。 */
  const applyExtras = (cfg: MCPServerConfig): MCPServerConfig => ({
    ...cfg,
    env: extraEnv.trim() ? { ...(cfg.env ?? {}), ...textToEnv(extraEnv) } : cfg.env,
    description: extraDesc.trim() || cfg.description || "",
    sourceType: extraSourceType || cfg.sourceType || "query",
    risk: extraRisk || cfg.risk || "medium",
  });

  /** 共享校验：名字合法且不重复（各模式解析后统一过这道关） */
  const checkName = (name: string): string | null => {
    if (!validServerName(name)) return `服务器名 "${name}" 不合法（小写字母/数字/连字符）`;
    if ((infos ?? []).some((i) => i.name === name)) return `服务器 "${name}" 已存在`;
    return null;
  };

  /** 添加：立即落盘生效（后端校验兜底），清空编辑器并刷新列表 */
  const commitParsed = async (parsed: ParsedServer) => {
    await agentApi.upsertMCPServer(parsed.name, parsed.config);
    setQuickText("");
    applyForm("", EMPTY_FORM);
    setJsonText("");
    setQuickErr(null);
    setFormErr(null);
    setJsonErr(null);
    clearExtras();
    setNotice(`已添加 "${parsed.name}"（点"探测"抓取工具清单后即可被检索）`);
    await refresh();
  };

  /** 启用/禁用：立即落盘生效（禁用后连接即回收，对 Agent 不可见） */
  const toggleEnabled = async (info: MCPServerInfo) => {
    setRowMsg(null);
    setRowBusy(true);
    try {
      await agentApi.setMCPServerEnabled(info.name, !info.enabled);
      await refresh();
    } catch (e) {
      setRowMsg({ name: info.name, kind: "err", text: errText(e) });
    } finally {
      setRowBusy(false);
    }
  };

  /** 删除：立即落盘生效（连接即回收，探测缓存清理）；条目消失即成功反馈 */
  const removeServer = async (name: string) => {
    setRowMsg(null);
    setRowBusy(true);
    try {
      await agentApi.removeMCPServer(name);
      await refresh();
    } catch (e) {
      setRowMsg({ name, kind: "err", text: errText(e) });
    } finally {
      setRowBusy(false);
    }
  };

  /** 模式切换：内容互转（quick 解析进表单、表单序列化进 JSON、JSON 解析回表单）。
   *  空输入安静切换；残留错误在切入时清除，避免上次错误误导。 */
  const switchMode = (next: EditorMode) => {
    if (next === mode) return;
    if (next === "quick") {
      // json → quick：解析出配置后**还原为命令行**回填（quick 框只收命令行，
      // 塞 JSON 文本会造成格式污染循环）；空 JSON 则清空 quick 框。
      if (mode === "json" && jsonText.trim()) {
        try {
          const parsed = parseServerJSON(jsonText, false);
          const cmdline = [parsed.config.command, ...(parsed.config.args ?? [])]
            .filter(Boolean)
            .join(" ");
          setQuickText(cmdline);
          setJsonErr(null);
        } catch (e) {
          setJsonErr(errText(e));
          return;
        }
      }
      setQuickErr(null);
      setMode("quick");
      return;
    }
    if (mode === "quick") {
      // 先解析一次（失败留在 quick 模式），再按目标分流
      let parsed: ParsedServer | null = null;
      if (quickText.trim()) {
        try {
          parsed = parseQuick(quickText);
          setQuickErr(null);
        } catch (e) {
          setQuickErr(errText(e));
          return;
        }
      }
      if (next === "json") {
        setJsonText(parsed ? toServerJSON(parsed.name, parsed.config) : "");
        setJsonErr(null);
      } else {
        // 切入手动配置：附加字段并入表单（表单接管完整编辑）后清空；
        // env 非法行在此报错并留在当前模式
        if (parsed) {
          try {
            applyForm(parsed.name, applyExtras(parsed.config));
            clearExtras();
          } catch (e) {
            setQuickErr(errText(e));
            return;
          }
        }
      }
      setMode(next);
      return;
    }
    if (mode === "form") {
      if (next === "json") {
        // env 文本解析 + 完整命令拆分；form 为空（无 command）时清空 JSON 框
        // 而不是写 {"mcp-server": {}} 的占位（占位会在 JSON→form 时把脏名字带回来）
        try {
          const env = textToEnv(envDraft);
          const argv = tokenizeCommand(form.command);
          if (argv.length === 0) {
            setJsonText("");
          } else {
            const normalized = { ...form, command: argv[0], args: argv.slice(1), env };
            setJsonText(toServerJSON(formName || deriveName(argv), normalized));
          }
          setJsonErr(null);
        } catch (e) {
          setFormErr(errText(e));
          return;
        }
      }
      setFormErr(null);
      setMode(next);
      return;
    }
    // json → form：非空 JSON 解析回填表单（空输入安静切换，不动现有表单）；
    // 草稿模式（strict=false）：容忍未填 command 的中间态，提交时再把关。
    // 附加字段一并并入表单后清空（env 非法行报错并留在 JSON 模式）
    if (jsonText.trim()) {
      try {
        const parsed = parseServerJSON(jsonText, false);
        applyForm(parsed.name, applyExtras(parsed.config));
        clearExtras();
        setJsonErr(null);
      } catch (e) {
        setJsonErr(errText(e));
        return;
      }
    }
    setFormErr(null);
    setMode(next);
  };

  const addServer = async () => {
    try {
      let parsed: ParsedServer;
      if (mode === "quick") {
        const p = parseQuick(quickText);
        parsed = { name: p.name, config: applyExtras(p.config) };
      } else if (mode === "json") {
        const p = parseServerJSON(jsonText);
        parsed = { name: p.name, config: applyExtras(p.config) };
      } else {
        // 手动配置：完整命令行在此拆分为 command+args（引号/转义与快速安装同款）；
        // env 文本解析（非法行在此报错）。
        const env = textToEnv(envDraft);
        const argv = tokenizeCommand(form.command);
        if (argv.length === 0) throw new Error("命令必填（可执行文件或 npx/uvx 等启动器）");
        parsed = {
          name: formName.trim().toLowerCase(),
          config: { ...form, command: argv[0], args: argv.slice(1), env },
        };
      }
      const nameErr = checkName(parsed.name);
      if (nameErr) throw new Error(nameErr);
      await commitParsed(parsed);
    } catch (e) {
      const msg = errText(e);
      if (mode === "quick") setQuickErr(msg);
      else if (mode === "json") setJsonErr(msg);
      else setFormErr(msg);
    }
  };

  const doProbe = async (name: string) => {
    setRowMsg(null);
    setProbing(name);
    try {
      // 配置已即改即存，直接探测（探测即授权：拉起外部进程抓工具清单）。
      await agentApi.probeMCPServer(name);
      setRowMsg({ name, kind: "ok", text: "工具清单已缓存" });
      await refresh();
    } catch (e) {
      setRowMsg({ name, kind: "err", text: errText(e) });
    } finally {
      setProbing(null);
    }
  };

  const modeErr = mode === "quick" ? quickErr : mode === "json" ? jsonErr : formErr;
  const setModeErr = mode === "quick" ? setQuickErr : mode === "json" ? setJsonErr : setFormErr;

  // 添加按钮可用性：当前模式有可提交的最小内容才可点（未填/填残时置灰）。
  // 手动配置要求命令非空（名字可在提交时校验，但无命令必然失败）；
  // quick/JSON 有内容才点亮（具体合法性提交时统一把关）。
  const addReady =
    mode === "quick"
      ? quickText.trim() !== ""
      : mode === "json"
        ? jsonText.trim() !== ""
        : form.command.trim() !== "";

  // 附加字段块：渲染在 quick/JSON 模式各自输入区下方（手动配置本身即完整表单）。
  // 两个模式不会同时展示，同一元素复用即可。
  const extrasBlock = (
    <>
      <div className="settings-field settings-field-block">
        <div className="settings-field-copy">
          <span className="settings-field-label">环境变量</span>
          <span className="settings-field-hint">
            KEY=VALUE 每行一条（最小权限凭证；支持 ${"${VAR}"} 引用）。与命令/JSON 中的 env 按键合并，此处优先。
          </span>
        </div>
        <div className="settings-field-control">
          <textarea
            className="settings-input mcp-quick-input"
            rows={2}
            value={extraEnv}
            onChange={(e) => {
              setExtraEnv(e.target.value);
              setModeErr(null);
            }}
            placeholder={"API_KEY=${MY_API_KEY}"}
            spellCheck={false}
          />
        </div>
      </div>
      <div className="settings-field settings-field-block">
        <div className="settings-field-copy">
          <span className="settings-field-label">描述</span>
          <span className="settings-field-hint">
            一句话能力描述（模型可见；英文对模型更友好，非强制）。填写后以这里为准。
          </span>
        </div>
        <div className="settings-field-control">
          <input
            className="settings-input"
            value={extraDesc}
            onChange={(e) => {
              setExtraDesc(e.target.value);
              setModeErr(null);
            }}
            placeholder="stock quotes and financial data"
            spellCheck={false}
          />
        </div>
      </div>
      <div className="settings-field settings-field-block">
        <div className="settings-field-copy">
          <span className="settings-field-label">信息源类型 / 风险</span>
          <span className="settings-field-hint">
            类型标注于系统消息索引；风险决定工具调用的审批级别。"自动" = 以解析结果为准（缺省 query / medium）。
          </span>
        </div>
        <div className="settings-field-control" style={{ display: "flex", gap: 8 }}>
          <select
            className="settings-select"
            value={extraSourceType}
            onChange={(e) => setExtraSourceType(e.target.value)}
          >
            <option value="">自动</option>
            {SOURCE_TYPES.map((t) => (
              <option key={t} value={t}>
                {t}
              </option>
            ))}
          </select>
          <select
            className="settings-select"
            value={extraRisk}
            onChange={(e) => setExtraRisk(e.target.value)}
          >
            <option value="">自动</option>
            {RISK_LEVELS.map((r) => (
              <option key={r} value={r}>
                {r}
              </option>
            ))}
          </select>
        </div>
      </div>
    </>
  );

  return (
    <div className="settings-page">
      <h2 className="settings-page-title">MCP 与工具</h2>
      <p className="settings-page-desc">
        MCP 服务器是外部系统的工具供给渠道：服务器级索引常驻系统消息，
        discover_tools 按需发现并把工具注册进会话。会话启动不拉起进程，命中后才懒启动；添加/删除/启停立即生效，启用后点"探测"抓取工具清单。
      </p>

      {/* 第一部分：添加 MCP 服务器（快速安装 / 手动配置 / JSON） */}
      <section className="settings-section">
        <div className="settings-section-title">添加 MCP 服务器</div>
      <div className="mcp-mode-seg" role="tablist" aria-label="添加方式">
        {(
          [
            ["quick", "快速安装"],
            ["form", "手动配置"],
            ["json", "JSON"],
          ] as [EditorMode, string][]
        ).map(([m, label]) => (
          <button
            key={m}
            type="button"
            role="tab"
            aria-selected={mode === m}
            className={`mcp-mode-seg-btn${mode === m ? " on" : ""}`}
            onClick={() => switchMode(m)}
          >
            {label}
          </button>
        ))}
      </div>

      {mode === "quick" && (
        <div className="settings-field settings-field-block">
          <div className="settings-field-copy">
            <span className="settings-field-label">命令或 JSON</span>
            <span className="settings-field-hint">
              粘贴启动命令即可：自动拆分 command/args 并推导服务器名；
              以 &#123; 开头时按 JSON 解析。环境变量、描述、类型/风险在下方补充。
            </span>
          </div>
          <div className="settings-field-control">
            <textarea
              className="settings-input mcp-quick-input"
              rows={3}
              value={quickText}
              onChange={(e) => {
                setQuickText(e.target.value);
                setQuickErr(null);
              }}
              placeholder="npx -y yahoo-finance-mcp@0.3.2"
              spellCheck={false}
            />
          </div>
        </div>
      )}
      {mode === "quick" && extrasBlock}

      {mode === "form" && (
        <>
          <div className="settings-field settings-field-block">
            <div className="settings-field-copy">
              <span className="settings-field-label">名字</span>
              <span className="settings-field-hint">
                编入工具名 mcp__&lt;name&gt;__&lt;tool&gt;，小写字母/数字/连字符。
              </span>
            </div>
            <div className="settings-field-control">
              <input
                className="settings-input"
                value={formName}
                onChange={(e) => {
                  setFormName(e.target.value);
                  setFormErr(null);
                }}
                placeholder="yahoo-finance"
                spellCheck={false}
              />
            </div>
          </div>
          <div className="settings-field settings-field-block">
            <div className="settings-field-copy">
              <span className="settings-field-label">命令</span>
              <span className="settings-field-hint">
                完整的启动命令（可执行文件或 npx/uvx 等启动器 + 参数，含参数用引号包裹亦可），提交时自动拆分。
              </span>
            </div>
            <div className="settings-field-control">
              <input
                className="settings-input"
                value={form.command}
                onChange={(e) => setForm({ ...form, command: e.target.value })}
                placeholder="npx -y yahoo-finance-mcp@0.3.2"
                spellCheck={false}
              />
            </div>
          </div>
          <div className="settings-field settings-field-block">
            <div className="settings-field-copy">
              <span className="settings-field-label">环境变量</span>
              <span className="settings-field-hint">
                KEY=VALUE 每行一条（最小权限凭证；支持 ${"${VAR}"} 引用）。
              </span>
            </div>
            <div className="settings-field-control">
              <textarea
                className="settings-input mcp-quick-input"
                rows={2}
                value={envDraft}
                onChange={(e) => {
                  setEnvDraft(e.target.value);
                  setFormErr(null);
                }}
                placeholder={"API_KEY=${MY_API_KEY}"}
                spellCheck={false}
              />
            </div>
          </div>
          <div className="settings-field settings-field-block">
            <div className="settings-field-copy">
              <span className="settings-field-label">描述</span>
              <span className="settings-field-hint">
                一句话能力描述（模型可见；英文对模型更友好，非强制）。
              </span>
            </div>
            <div className="settings-field-control">
              <input
                className="settings-input"
                value={form.description ?? ""}
                onChange={(e) => setForm({ ...form, description: e.target.value })}
                placeholder="stock quotes and financial data"
                spellCheck={false}
              />
            </div>
          </div>
          <div className="settings-field settings-field-block">
            <div className="settings-field-copy">
              <span className="settings-field-label">信息源类型 / 风险</span>
              <span className="settings-field-hint">
                类型标注于系统消息索引；风险决定工具调用的审批级别。
              </span>
            </div>
            <div className="settings-field-control" style={{ display: "flex", gap: 8 }}>
              <select
                className="settings-select"
                value={form.sourceType ?? "query"}
                onChange={(e) => setForm({ ...form, sourceType: e.target.value })}
              >
                {SOURCE_TYPES.map((t) => (
                  <option key={t} value={t}>
                    {t}
                  </option>
                ))}
              </select>
              <select
                className="settings-select"
                value={form.risk ?? "medium"}
                onChange={(e) => setForm({ ...form, risk: e.target.value })}
              >
                {RISK_LEVELS.map((r) => (
                  <option key={r} value={r}>
                    {r}
                  </option>
                ))}
              </select>
            </div>
          </div>
        </>
      )}

      {mode === "json" && (
        <div className="settings-field settings-field-block">
          <div className="settings-field-copy">
            <span className="settings-field-label">完整配置</span>
            <span className="settings-field-hint">
              支持 {"{\"server-name\": {...}}"} 或
              {" {\"mcpServers\": {\"server-name\": {...}}}"}
              （Claude Desktop 格式）。下方输入框用于补充或覆盖 JSON 中的
              env/描述/类型/风险。
            </span>
          </div>
          <div className="settings-field-control">
            <textarea
              className="settings-input mcp-quick-input mcp-json-input"
              rows={8}
              value={jsonText}
              onChange={(e) => {
                setJsonText(e.target.value);
                setJsonErr(null);
              }}
              placeholder={'{\n  "yahoo-finance": {\n    "command": "npx",\n    "args": ["-y", "yahoo-finance-mcp@0.3.2"]\n  }\n}'}
              spellCheck={false}
            />
          </div>
        </div>
      )}
      {mode === "json" && extrasBlock}

      {modeErr && <div className="settings-error">{modeErr}</div>}
      {notice && <div className="settings-notice">{notice}</div>}
      <div className="dialog-actions mcp-add-actions">
        <button
          className="dialog-btn primary mcp-add-btn"
          disabled={!addReady}
          onClick={() => {
            setModeErr(null);
            void addServer();
          }}
        >
          添加
        </button>
      </div>
      </section>

      {/* 第二部分：MCP 列表（独立分块，已配置服务器；列表过长不顶表单） */}
      <section className="settings-section">
      <div className="settings-section-title">
        已配置 MCP 列表（{infos?.length ?? 0}）
      </div>
      {error && <div className="settings-error">{error}</div>}
      {infos === null ? (
        <div className="settings-loading">加载中…</div>
      ) : infos.length === 0 ? (
        <div className="skills-empty">
          <Plug size={20} />
          <span>还没有配置 MCP 服务器。上方添加。</span>
        </div>
      ) : (
        infos.map((info) => {
          return (
            <div key={info.name} className={`skill-item${info.enabled ? "" : " disabled"}`}>
              <div className="skill-item-body">
              <div className="skill-item-main">
                <div className="skill-item-head">
                  <code className="skill-item-name">{info.name}</code>
                  <button
                    className={`switch${info.enabled ? " on" : ""}`}
                    role="switch"
                    aria-checked={info.enabled}
                    disabled={rowBusy}
                    title={info.enabled ? "禁用（立即生效）" : "启用（立即生效）"}
                    onClick={() => void toggleEnabled(info)}
                  >
                    <span className="switch-thumb" />
                  </button>
                  {info.sourceType && (
                    <span className="skill-item-tag">{info.sourceType}</span>
                  )}
                  <span className="skill-item-tag" title="工具默认风险级别">
                    {info.risk}
                  </span>
                </div>
                {info.description && (
                  <div className="skill-item-desc">{info.description}</div>
                )}
                <div className="skill-item-meta">
                  <code>{info.command}</code>
                  <span>·</span>
                  <span>
                    {info.toolCount > 0 ? `${info.toolCount} 个工具` : "未探测"}
                  </span>
                </div>
              </div>
              {rowMsg && rowMsg.name === info.name && (
                <div className={`settings-${rowMsg.kind === "ok" ? "notice" : "error"} mcp-row-msg`}>
                  {rowMsg.text}
                </div>
              )}
              </div>
              <div className="skill-item-actions">
                <button
                  className="mcp-icon-btn"
                  disabled={!info.enabled || probing !== null || rowBusy}
                  title={info.enabled ? "探测：抓取工具清单缓存" : "启用后才能探测"}
                  onClick={() => void doProbe(info.name)}
                >
                  {probing === info.name ? (
                    <RefreshCw size={12} className="ws-spin" />
                  ) : (
                    <Zap size={12} />
                  )}
                  探测
                </button>
                <button
                  className="mcp-icon-btn danger"
                  disabled={rowBusy}
                  title="删除（立即生效，连接即回收）"
                  onClick={() => void removeServer(info.name)}
                >
                  <Trash2 size={12} />
                </button>
              </div>
            </div>
          );
        })
      )}
      </section>
    </div>
  );
}
