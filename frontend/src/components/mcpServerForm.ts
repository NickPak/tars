/**
 * MCP 添加服务器的三态编辑器纯函数助手（快速安装/手动配置/JSON）。
 * 解析规则对齐 Reasonix 的 MCPServerSettingsEditor，适配 tars 的
 * ServerConfig（command + args 分离、仅 stdio、扩展 description/
 * sourceType/enabled/risk 字段）。全部纯函数，便于单测。
 */
import type { MCPServerConfig } from "../types";

/** 解析结果：服务器名 + 配置 */
export interface ParsedServer {
  name: string;
  config: MCPServerConfig;
}

const NAME_RE = /^[a-z0-9]([a-z0-9-]*[a-z0-9])?$/;

/** 服务器名合法性（与后端 ValidateName 同规则） */
export function validServerName(name: string): boolean {
  return NAME_RE.test(name);
}

/**
 * tokenizeCommand 把命令行拆成 argv（支持单/双引号与反斜杠转义）。
 * 快速安装模式把整行命令拆为 command + args。
 */
export function tokenizeCommand(line: string): string[] {
  const out: string[] = [];
  let cur = "";
  let quote: '"' | "'" | null = null;
  let escaped = false;
  for (const ch of line.trim()) {
    if (escaped) {
      cur += ch;
      escaped = false;
      continue;
    }
    if (ch === "\\" && quote !== "'") {
      escaped = true;
      continue;
    }
    if (quote) {
      if (ch === quote) quote = null;
      else cur += ch;
      continue;
    }
    if (ch === '"' || ch === "'") {
      quote = ch;
      continue;
    }
    if (/\s/.test(ch)) {
      if (cur) {
        out.push(cur);
        cur = "";
      }
      continue;
    }
    cur += ch;
  }
  if (cur) out.push(cur);
  return out;
}

const LAUNCHERS = new Set(["npx", "bunx", "uvx", "node"]);
const RESERVED = new Set([
  "npx", "uvx", "uv", "node", "bunx",
  "python", "python3", "py", "mcp-server",
]);

/** 第一个非旗标操作数（跳过 -y、--yes 等选项及其值无法区分，故仅跳旗标本身） */
function firstOperand(argv: string[]): string | undefined {
  return argv.find((a) => !a.startsWith("-"));
}

/**
 * deriveName 从命令推导服务器名（npx/bunx/uvx → 首个包名，python -m → 模块名，
 * 去掉 @version 与扩展名，清洗为小写连字符）。
 */
export function deriveName(argv: string[]): string {
  const executable =
    argv[0]?.split(/[\\/]/).pop()?.toLowerCase().replace(/\.(cmd|exe|bat)$/i, "") ?? "";
  let candidate = argv[0] ?? "mcp-server";
  if (LAUNCHERS.has(executable)) {
    candidate = firstOperand(argv.slice(1)) ?? candidate;
  } else if (["python", "python3", "py"].includes(executable)) {
    const m = argv.findIndex((a) => a === "-m");
    candidate =
      (m >= 0 ? argv[m + 1] : firstOperand(argv.slice(1))) ?? candidate;
  } else if (executable === "uv" && argv[1] === "run") {
    candidate = firstOperand(argv.slice(2)) ?? candidate;
  }
  const base = candidate.split(/[\\/]/).pop() ?? candidate;
  const unversioned = base.replace(/@[^@]+$/, "").replace(/\.(cmd|exe|bat)$/i, "");
  const sanitized = unversioned
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, "-")
    .replace(/^-+|-+$/g, "");
  return sanitized && /[a-z0-9]/.test(sanitized) && !RESERVED.has(sanitized)
    ? sanitized
    : "mcp-server";
}

const DEFAULTS: Pick<MCPServerConfig, "description" | "sourceType" | "enabled" | "risk"> = {
  description: "",
  sourceType: "query",
  enabled: true, // 添加即启用（显式添加动作即"显式开放"）
  risk: "medium",
};

/** JSON 里允许出现的键（Claude Desktop 标准键 + tars 扩展键） */
const JSON_KEYS = new Set([
  "command", "args", "env",
  "description", "sourceType", "enabled", "risk",
]);

function isRecord(v: unknown): v is Record<string, unknown> {
  return typeof v === "object" && v !== null && !Array.isArray(v);
}

function asString(v: unknown, key: string): string {
  if (v == null) return "";
  if (typeof v !== "string") throw new Error(`字段 "${key}" 必须是字符串`);
  return v;
}

function asStringArray(v: unknown, key: string): string[] {
  if (v == null) return [];
  if (!Array.isArray(v) || !v.every((x) => typeof x === "string")) {
    throw new Error(`字段 "${key}" 必须是字符串数组`);
  }
  return v as string[];
}

function asStringRecord(v: unknown, key: string): Record<string, string> {
  if (v == null) return {};
  if (!isRecord(v) || !Object.values(v).every((x) => typeof x === "string")) {
    throw new Error(`字段 "${key}" 必须是字符串键值表`);
  }
  return v as Record<string, string>;
}

/**
 * parseServerJSON 解析 JSON 配置：支持 {"server-name": {...}} 与
 * {"mcpServers": {"server-name": {...}}}（Claude Desktop 约定）。
 * 恰好一个服务器；未知键报错（防拼写错误静默丢失）。
 *
 * strict（默认 true，提交路径）：command 必填；
 * strict=false（模式切换草稿路径）：容忍空 command 的中间态——
 * 切换不应做提交级校验，残缺内容在最终"添加"时统一把关。
 */
export function parseServerJSON(raw: string, strict = true): ParsedServer {
  let parsed: unknown;
  try {
    parsed = JSON.parse(raw);
  } catch {
    throw new Error("JSON 格式错误");
  }
  if (!isRecord(parsed)) throw new Error("JSON 须为对象");
  const container = isRecord(parsed.mcpServers) ? parsed.mcpServers : parsed;
  const entries = Object.entries(container);
  if (entries.length !== 1) throw new Error("JSON 须恰好包含一个服务器");
  const [name, value] = entries[0];
  if (!validServerName(name)) {
    throw new Error(`服务器名 "${name}" 不合法（小写字母/数字/连字符）`);
  }
  if (!isRecord(value)) throw new Error(`服务器 "${name}" 的配置须为对象`);
  for (const k of Object.keys(value)) {
    if (!JSON_KEYS.has(k)) throw new Error(`不支持的字段 "${k}"`);
  }
  const command = asString(value.command, "command").trim();
  if (strict && !command) throw new Error("command 必填");
  const risk = asString(value.risk, "risk") || DEFAULTS.risk!;
  if (!["low", "medium", "high"].includes(risk)) {
    throw new Error(`risk 须为 low/medium/high，得到 "${risk}"`);
  }
  return {
    name,
    config: {
      command,
      args: asStringArray(value.args, "args"),
      env: asStringRecord(value.env, "env"),
      description: asString(value.description, "description") || DEFAULTS.description!,
      sourceType: asString(value.sourceType, "sourceType") || DEFAULTS.sourceType!,
      enabled: typeof value.enabled === "boolean" ? value.enabled : DEFAULTS.enabled!,
      risk,
    },
  };
}

/**
 * parseQuick 解析快速安装输入：
 *   - "{" 开头 → 走 JSON 解析；
 *   - http(s):// → 报错（v1 仅支持 stdio 本地进程）；
 *   - 其余按命令行拆分（command + args），名字自动推导。
 */
export function parseQuick(raw: string): ParsedServer {
  const text = raw.trim();
  if (!text) throw new Error("请输入命令或 JSON");
  if (text.startsWith("{")) return parseServerJSON(text);
  if (/^https?:\/\//i.test(text)) {
    throw new Error("暂不支持远程 URL 服务器（当前版本仅支持本地 stdio 进程）");
  }
  const argv = tokenizeCommand(text);
  if (argv.length === 0) throw new Error("命令为空");
  return {
    name: deriveName(argv),
    config: { command: argv[0], args: argv.slice(1), env: {}, ...DEFAULTS },
  };
}

/** toServerJSON 把手动配置的草稿序列化为 JSON 文本（模式切换用） */
export function toServerJSON(name: string, cfg: MCPServerConfig): string {
  const body: Record<string, unknown> = { command: cfg.command };
  if ((cfg.args ?? []).length > 0) body.args = cfg.args;
  if (cfg.env && Object.keys(cfg.env).length > 0) body.env = cfg.env;
  if (cfg.description) body.description = cfg.description;
  if (cfg.sourceType && cfg.sourceType !== DEFAULTS.sourceType) {
    body.sourceType = cfg.sourceType;
  }
  if (cfg.enabled === false) body.enabled = false;
  if (cfg.risk && cfg.risk !== DEFAULTS.risk) body.risk = cfg.risk;
  return JSON.stringify({ [name || "mcp-server"]: body }, null, 2);
}

/** envToText：KEY=VALUE 每行一条（手动配置的 env 编辑） */
export function envToText(env: Record<string, string> | undefined): string {
  return Object.entries(env ?? {})
    .map(([k, v]) => `${k}=${v}`)
    .join("\n");
}

/** textToEnv：解析 KEY=VALUE 行；空行与 # 注释忽略，非法行报错 */
export function textToEnv(text: string): Record<string, string> {
  const env: Record<string, string> = {};
  for (const [i, raw] of text.split("\n").entries()) {
    const line = raw.trim();
    if (!line || line.startsWith("#")) continue;
    const eq = line.indexOf("=");
    if (eq <= 0) throw new Error(`第 ${i + 1} 行须为 KEY=VALUE 形式`);
    env[line.slice(0, eq).trim()] = line.slice(eq + 1).trim();
  }
  return env;
}
