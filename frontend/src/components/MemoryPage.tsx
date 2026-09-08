import { useCallback, useEffect, useState } from "react";
import { Archive, Check, Pencil, RefreshCw, Trash2, X } from "lucide-react";
import { agentApi } from "../services/agentApi";
import type { AppConfig, MemoryAuditView, MemoryFact, MemoryFactsView } from "../types";
import { ConfirmDialog } from "./Dialog";

const TYPE_LABELS: Record<MemoryFact["type"], string> = {
  user: "用户",
  feedback: "反馈",
  project: "项目",
  reference: "参考",
  lesson: "教训",
};

function errText(e: unknown): string {
  return e instanceof Error ? e.message : String(e);
}

/** 过期判定与后端 Expired() 同口径（expiresAt < 今天） */
function isExpired(f: MemoryFact): boolean {
  if (!f.expiresAt) return false;
  return f.expiresAt < new Date().toISOString().slice(0, 10);
}

/**
 * 记忆页 —— 跨会话事实记忆的可审计面板（六家共识 7：完全用户可见）。
 * 数据即改即存（不走设置面板底部的 draft 保存流）：
 * 编辑正文 / 删除（归档留痕，可恢复）都直接落盘并重建索引。
 */
export default function MemoryPage({
  draft,
  update,
}: {
  draft: AppConfig;
  update: (fn: (d: AppConfig) => AppConfig) => void;
}) {
  const [view, setView] = useState<MemoryFactsView | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(false);
  const [editing, setEditing] = useState<{ scope: string; projectId: string; subject: string; body: string } | null>(null);
  const [deleteTarget, setDeleteTarget] = useState<{ scope: string; projectId: string; subject: string } | null>(null);
  // 审计视图：key 为 "global" 或 projectId
  const [audit, setAudit] = useState<Record<string, MemoryAuditView>>({});
  const [auditOpen, setAuditOpen] = useState<Record<string, boolean>>({});

  const load = useCallback(() => {
    setLoading(true);
    agentApi
      .listMemoryFacts()
      .then((v) => {
        setView(v);
        setError(null);
      })
      .catch((e) => setError(errText(e)))
      .finally(() => setLoading(false));
  }, []);

  useEffect(load, [load]);

  const memCfg = draft.memory ?? { enabled: true, maxIndexBytes: 8192 };

  const handleSaveEdit = async () => {
    if (!editing) return;
    try {
      await agentApi.updateMemoryFact(editing.scope, editing.projectId, editing.subject, editing.body);
      setEditing(null);
      load();
    } catch (e) {
      setError(errText(e));
    }
  };

  const handleDelete = async () => {
    if (!deleteTarget) return;
    try {
      await agentApi.forgetMemoryFact(deleteTarget.scope, deleteTarget.projectId, deleteTarget.subject);
      setDeleteTarget(null);
      load();
    } catch (e) {
      setError(errText(e));
    }
  };

  const handleAdopt = async (projectId: string, subject: string) => {
    try {
      await agentApi.adoptMemoryCandidate(projectId, subject);
      load();
    } catch (e) {
      setError(errText(e));
    }
  };

  const handleReject = async (projectId: string, subject: string) => {
    try {
      await agentApi.rejectMemoryCandidate(projectId, subject);
      load();
    } catch (e) {
      setError(errText(e));
    }
  };

  const handleAdoptAll = async (projectId: string) => {
    try {
      await agentApi.adoptAllMemoryCandidates(projectId);
      load();
    } catch (e) {
      setError(errText(e));
    }
  };

  const handleRejectAll = async (projectId: string) => {
    try {
      await agentApi.rejectAllMemoryCandidates(projectId);
      load();
    } catch (e) {
      setError(errText(e));
    }
  };

  const toggleAudit = async (key: string, scope: string, projectId: string) => {
    const open = !auditOpen[key];
    setAuditOpen({ ...auditOpen, [key]: open });
    if (open && !audit[key]) {
      try {
        const v = await agentApi.listMemoryAudit(scope, projectId);
        setAudit({ ...audit, [key]: v });
      } catch (e) {
        setError(errText(e));
      }
    }
  };

  /** 审计区块：归档（删除/覆盖旧值）+ 拒绝（候选审计），只读 */
  const renderAudit = (key: string) => {
    if (!auditOpen[key]) return null;
    const a = audit[key];
    if (!a) return <div className="mem-audit">加载中…</div>;
    const archived = a.archived ?? [];
    const rejected = a.rejected ?? [];
    if (archived.length === 0 && rejected.length === 0) {
      return <div className="mem-audit">暂无留痕。</div>;
    }
    const renderRow = (tag: string, f: MemoryFact) => (
      <div key={tag + f.subject} className="mem-fact mem-audit-row">
        <div className="mem-fact-head">
          <span className="mem-audit-tag">{tag}</span>
          <span className={`mem-type mem-type-${f.type}`}>{TYPE_LABELS[f.type]}</span>
          <span className="mem-subject">{f.subject}</span>
        </div>
        <div className="mem-body">{f.body}</div>
      </div>
    );
    return (
      <div className="mem-audit">
        {archived.map((f) => renderRow("归档", f))}
        {rejected.map((f) => renderRow("已拒绝", f))}
      </div>
    );
  };

  const renderFact = (scope: string, projectId: string, f: MemoryFact) => {
    const isEditing =
      editing !== null && editing.scope === scope && editing.projectId === projectId && editing.subject === f.subject;
    return (
      <div key={scope + projectId + f.subject} className={`mem-fact${isExpired(f) ? " expired" : ""}`}>
        <div className="mem-fact-head">
          <span className={`mem-type mem-type-${f.type}`}>{TYPE_LABELS[f.type]}</span>
          <span className="mem-subject" title={`${f.subject} · 来源 ${f.writtenBy || "未知"} · 确认于 ${f.lastConfirmed}`}>
            {f.subject}
          </span>
          {isExpired(f) && <span className="mem-expired-tag">已过期</span>}
          <span className="mem-actions">
            <button
              className="ws-icon-btn"
              title="编辑正文"
              onClick={() =>
                setEditing(isEditing ? null : { scope, projectId, subject: f.subject, body: f.body })
              }
            >
              <Pencil size={13} />
            </button>
            <button
              className="ws-icon-btn"
              title="删除（归档留痕，可恢复）"
              onClick={() => setDeleteTarget({ scope, projectId, subject: f.subject })}
            >
              <Trash2 size={13} />
            </button>
          </span>
        </div>
        {isEditing ? (
          <div className="mem-edit">
            <textarea
              className="settings-input mem-edit-area"
              value={editing!.body}
              rows={3}
              onChange={(e) => setEditing({ ...editing!, body: e.target.value })}
            />
            <div className="mem-edit-actions">
              <button className="dialog-btn secondary" onClick={() => setEditing(null)}>
                取消
              </button>
              <button className="dialog-btn primary" onClick={() => void handleSaveEdit()}>
                保存
              </button>
            </div>
          </div>
        ) : (
          <div className="mem-body">{f.body}</div>
        )}
      </div>
    );
  };

  const globals = view?.global ?? [];
  const projects = view?.projects ?? [];
  const empty = !loading && globals.length === 0 && projects.length === 0;

  return (
    <div className="settings-page">
      <h2 className="settings-page-title">记忆</h2>
      <p className="settings-page-desc">
        Agent 跨会话记住的事实（remember 工具写入 + 你可直接编辑）。索引注入每轮对话，超上限退化为按需检索；编辑与删除立即生效，删除走归档可恢复。
      </p>

      <section className="settings-section">
        <div className="settings-section-title">功能</div>
        <div className="settings-field">
          <div className="settings-field-copy">
            <span className="settings-field-label">启用记忆</span>
            <span className="settings-field-hint">关闭后记忆块不注入对话，remember/recall 工具不可用。</span>
          </div>
          <button
            className={`switch${memCfg.enabled ? " on" : ""}`}
            role="switch"
            aria-checked={memCfg.enabled}
            onClick={() =>
              update((d) => ({
                ...d,
                memory: { ...memCfg, enabled: !memCfg.enabled },
              }))
            }
          >
            <span className="switch-thumb" />
          </button>
        </div>
        <div className="settings-field">
          <div className="settings-field-copy">
            <span className="settings-field-label">索引注入上限</span>
            <span className="settings-field-hint">字节数。超出截断并提示用 recall 工具检索。</span>
          </div>
          <input
            className="settings-input small"
            type="number"
            min={1024}
            value={memCfg.maxIndexBytes}
            onChange={(e) =>
              update((d) => ({
                ...d,
                memory: { ...memCfg, maxIndexBytes: Math.max(1024, e.target.valueAsNumber || 8192) },
              }))
            }
          />
        </div>
      </section>

      <section className="settings-section">
        <div className="settings-section-title mem-section-head">
          全局记忆（{globals.length}）
          <button className="ws-icon-btn" title="刷新" onClick={load}>
            <RefreshCw size={13} className={loading ? "ws-spin" : ""} />
          </button>
          <button
            className={`ws-icon-btn${auditOpen["global"] ? " mem-audit-on" : ""}`}
            title="留痕（归档 + 拒绝审计）"
            onClick={() => void toggleAudit("global", "global", "")}
          >
            <Archive size={13} />
          </button>
        </div>
        {error && <div className="settings-load-error">{error}</div>}
        {globals.map((f) => renderFact("global", "", f))}
        {renderAudit("global")}
      </section>

      {projects.map((p) => (
        <section className="settings-section" key={p.projectId}>
          <div className="settings-section-title mem-section-head">
            项目：{p.title}（{p.facts.length}）
            <button
              className={`ws-icon-btn${auditOpen[p.projectId] ? " mem-audit-on" : ""}`}
              title="留痕（归档 + 拒绝审计）"
              onClick={() => void toggleAudit(p.projectId, "project", p.projectId)}
            >
              <Archive size={13} />
            </button>
          </div>
          {p.facts.map((f) => renderFact("project", p.projectId, f))}
          {renderAudit(p.projectId)}

          {/* 候选区：压缩联动产出的建议，采纳前不生效（采纳制） */}
          {(p.candidates?.length ?? 0) > 0 && (
            <div className="mem-candidates">
              <div className="mem-candidates-title">
                候选（{p.candidates!.length}）—— 由历史对话提炼，采纳后才生效
                <span className="mem-candidates-batch">
                  <button className="dialog-btn secondary" onClick={() => void handleAdoptAll(p.projectId)}>
                    全部采纳
                  </button>
                  <button className="dialog-btn secondary" onClick={() => void handleRejectAll(p.projectId)}>
                    全部拒绝
                  </button>
                </span>
              </div>
              {p.candidates!.map((c) => (
                <div key={c.subject} className="mem-fact mem-candidate">
                  <div className="mem-fact-head">
                    <span className={`mem-type mem-type-${c.type}`}>{TYPE_LABELS[c.type]}</span>
                    <span
                      className="mem-subject"
                      title={`${c.subject} · 来源 ${c.source}`}
                    >
                      {c.subject}
                    </span>
                    <span className="mem-actions">
                      <button
                        className="ws-icon-btn mem-adopt"
                        title="采纳（转为正式记忆，进入索引）"
                        onClick={() => void handleAdopt(p.projectId, c.subject)}
                      >
                        <Check size={13} />
                      </button>
                      <button
                        className="ws-icon-btn"
                        title="拒绝（不再重复提议）"
                        onClick={() => void handleReject(p.projectId, c.subject)}
                      >
                        <X size={13} />
                      </button>
                    </span>
                  </div>
                  <div className="mem-body">{c.body}</div>
                </div>
              ))}
            </div>
          )}
        </section>
      ))}

      {empty && (
        <div className="mem-empty">暂无记忆。对话中说"记住我……"，Agent 会在这里留下事实。</div>
      )}

      <ConfirmDialog
        open={deleteTarget !== null}
        message={`确定删除记忆「${deleteTarget?.subject ?? ""}」？旧值将归档保留，可恢复。`}
        onCancel={() => setDeleteTarget(null)}
        onConfirm={() => void handleDelete()}
      />
    </div>
  );
}
