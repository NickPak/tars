import { create } from "zustand";
import { agentApi } from "../services/agentApi";
import { useChatStore } from "./chatStore";
import type { FileEntry } from "../types";

/** 名称排序方向 */
export type NameOrder = "asc" | "desc";

interface WorkspaceState {
  /** 当前会话工作区的文件树 */
  tree: FileEntry[];
  loading: boolean;
  error: string | null;
  /** 递增触发"全部展开/折叠"（FilesTab 监听此值，配合 expandAll 标志） */
  expandKey: number;
  /** expandKey 对应的目标状态：true=展开全部 false=折叠全部 */
  expandAll: boolean;
  /** 名称排序方向（目录始终排在文件前，组内按名称排） */
  nameOrder: NameOrder;

  /** 重新加载当前会话工作区的文件树 */
  refresh: () => Promise<void>;
  /** 展开/折叠全部目录（二合一开关，按当前目标状态翻转） */
  toggleExpandAll: () => void;
  /** 切换名称排序方向 */
  cycleNameOrder: () => void;
}

/** 按名称排序（原地递归；目录始终排在文件前） */
export function sortTree(entries: FileEntry[], order: NameOrder): FileEntry[] {
  entries.sort((a, b) => {
    if (a.isDir !== b.isDir) return a.isDir ? -1 : 1;
    const c = a.name.localeCompare(b.name);
    return order === "asc" ? c : -c;
  });
  for (const e of entries) {
    if (e.children) sortTree(e.children, order);
  }
  return entries;
}

export const useWorkspaceStore = create<WorkspaceState>((set, get) => ({
  tree: [],
  loading: false,
  error: null,
  expandKey: 0,
  expandAll: true,
  nameOrder: "asc",

  refresh: async () => {
    const activeId = useChatStore.getState().activeId;
    if (!activeId) {
      set({ tree: [], loading: false, error: null });
      return;
    }
    set({ loading: true, error: null });
    try {
      const entries = await agentApi.listWorkspaceFiles(activeId);
      set({ tree: sortTree(entries ?? [], get().nameOrder), loading: false });
    } catch (e) {
      set({
        error: e instanceof Error ? e.message : String(e),
        tree: [],
        loading: false,
      });
    }
  },

  toggleExpandAll: () =>
    set((s) => ({
      expandKey: s.expandKey + 1,
      expandAll: !s.expandAll,
    })),

  cycleNameOrder: () =>
    set((s) => {
      const next: NameOrder = s.nameOrder === "asc" ? "desc" : "asc";
      // 对已加载的树原地重排，无需重新请求后端
      return { nameOrder: next, tree: sortTree(s.tree, next) };
    }),
}));
