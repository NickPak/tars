import { create } from "zustand";
import { agentApi } from "../services/agentApi";
import { useChatStore } from "./chatStore";
import type { FileEntry } from "../types";

interface WorkspaceState {
  /** 当前会话工作区的文件树 */
  tree: FileEntry[];
  loading: boolean;
  error: string | null;
  /** 递增触发"折叠全部"（FilesTab 监听此值） */
  collapseKey: number;

  /** 重新加载当前会话工作区的文件树 */
  refresh: () => Promise<void>;
  /** 折叠全部目录 */
  collapseAll: () => void;
}

export const useWorkspaceStore = create<WorkspaceState>((set) => ({
  tree: [],
  loading: false,
  error: null,
  collapseKey: 0,

  refresh: async () => {
    const activeId = useChatStore.getState().activeId;
    if (!activeId) {
      set({ tree: [], loading: false, error: null });
      return;
    }
    set({ loading: true, error: null });
    try {
      const entries = await agentApi.listWorkspaceFiles(activeId);
      set({ tree: entries ?? [], loading: false });
    } catch (e) {
      set({
        error: e instanceof Error ? e.message : String(e),
        tree: [],
        loading: false,
      });
    }
  },

  collapseAll: () => set((s) => ({ collapseKey: s.collapseKey + 1 })),
}));
