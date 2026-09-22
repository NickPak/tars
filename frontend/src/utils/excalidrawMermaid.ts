import type { ExcalidrawElement } from "@excalidraw/excalidraw/element/types";

/**
 * Excalidraw scene → Mermaid 确定性转换器（无 AI 介入，同画布恒定同输出）。
 *
 * 转换规则（利用 Excalidraw 的绑定关系还原图结构）：
 *   矩形/椭圆/菱形（含绑定文本） → 节点（[]/(())/{}）
 *   两端均吸附到图形的箭头      → 边（-->，绑定文本 → |标签|）
 *
 * 确定性子集之外的元素（自由手绘/图片/未绑定箭头/孤立文本等）无法
 * 无损映射，整体判定不可转换并返回原因——调用方据此置灰按钮并引导
 * 走图片通道。
 */

export type ConvertResult =
  | { ok: true; mermaid: string }
  | { ok: false; reason: string };

const NODE_TYPES = new Set(["rectangle", "ellipse", "diamond"]);

/** Mermaid 标签转义：引号转实体，换行转 <br> */
function escLabel(s: string): string {
  return s.replace(/"/g, "#quot;").replace(/\n+/g, "<br>").trim();
}

export function sceneToMermaid(elements: readonly ExcalidrawElement[]): ConvertResult {
  const els = elements.filter((e) => !e.isDeleted);
  if (els.length === 0) return { ok: false, reason: "画布为空" };

  const byId = new Map(els.map((e) => [e.id, e]));

  // frame 只是分组容器，不参与图结构，跳过；其余未知元素类型不可转换
  for (const e of els) {
    if (e.type === "frame") continue;
    if (NODE_TYPES.has(e.type) || e.type === "arrow" || e.type === "text") continue;
    return { ok: false, reason: "包含自由手绘/图片等不可转换内容，请改用图片" };
  }

  // 文本必须绑定到图形（节点标签）或箭头（边标签）
  const nodeLabel = new Map<string, string>();
  const edgeLabel = new Map<string, string>();
  for (const e of els) {
    if (e.type !== "text") continue;
    const containerId = (e as { containerId?: string | null }).containerId;
    const container = containerId ? byId.get(containerId) : undefined;
    if (!container) {
      return { ok: false, reason: "存在未绑定图形的孤立文本，请把文字写进图形里（双击图形）" };
    }
    if (NODE_TYPES.has(container.type)) nodeLabel.set(container.id, e.text);
    else if (container.type === "arrow") edgeLabel.set(container.id, e.text);
  }

  const nodes = els.filter((e) => NODE_TYPES.has(e.type));
  if (nodes.length === 0) {
    return { ok: false, reason: "没有可转换的图形（矩形/椭圆/菱形 + 吸附箭头）" };
  }
  // Mermaid 节点 ID：按画布顺序编号（确定性）
  const mid = new Map<string, string>();
  nodes.forEach((n, i) => mid.set(n.id, `N${i + 1}`));

  // 边：箭头必须两端都吸附到节点
  const arrows = els.filter((e) => e.type === "arrow");
  for (const a of arrows) {
    const b = a as {
      startBinding?: { elementId: string } | null;
      endBinding?: { elementId: string } | null;
    };
    const s = b.startBinding?.elementId;
    const t = b.endBinding?.elementId;
    if (!s || !t || !mid.has(s) || !mid.has(t)) {
      return { ok: false, reason: "存在未吸附图形的箭头，请把箭头两端拖到图形上（出现吸附框）" };
    }
  }

  const lines = ["flowchart TD"];
  for (const n of nodes) {
    const id = mid.get(n.id)!;
    const label = escLabel(nodeLabel.get(n.id) ?? id);
    switch (n.type) {
      case "diamond":
        lines.push(`    ${id}{"${label}"}`);
        break;
      case "ellipse":
        lines.push(`    ${id}(("${label}"))`);
        break;
      default:
        lines.push(`    ${id}["${label}"]`);
    }
  }
  for (const a of arrows) {
    const b = a as {
      startBinding?: { elementId: string } | null;
      endBinding?: { elementId: string } | null;
    };
    const s = mid.get(b.startBinding!.elementId)!;
    const t = mid.get(b.endBinding!.elementId)!;
    const label = edgeLabel.get(a.id)?.trim();
    lines.push(label ? `    ${s} -->|"${escLabel(label)}"| ${t}` : `    ${s} --> ${t}`);
  }
  return { ok: true, mermaid: lines.join("\n") };
}
