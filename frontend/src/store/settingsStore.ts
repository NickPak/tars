import { create } from "zustand";

/** 设置面板的分类页签。占位页签（skills/mcp/appearance）对应规划中功能 */
export type SettingsTab =
  | "general"
  | "model"
  | "agent"
  | "trace"
  | "skills"
  | "mcp"
  | "appearance"
  | "about";

interface SettingsState {
  open: boolean;
  tab: SettingsTab;
  openSettings: (tab?: SettingsTab) => void;
  closeSettings: () => void;
  setTab: (tab: SettingsTab) => void;
}

export const useSettingsStore = create<SettingsState>((set) => ({
  open: false,
  tab: "general",
  openSettings: (tab) => set({ open: true, ...(tab ? { tab } : {}) }),
  closeSettings: () => set({ open: false }),
  setTab: (tab) => set({ tab }),
}));
