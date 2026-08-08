import { create } from "zustand";

interface LayoutState {
  /** 侧边栏宽度（px） */
  sidebarWidth: number;
  /** 侧边栏是否折叠 */
  sidebarCollapsed: boolean;
  /** 右侧工作区面板是否可见 */
  workspaceVisible: boolean;
  /** 右侧工作区面板宽度（px） */
  workspaceWidth: number;

  toggleSidebar: () => void;
  toggleWorkspace: () => void;
  setSidebarWidth: (w: number) => void;
  setWorkspaceWidth: (w: number) => void;
}

export const useLayoutStore = create<LayoutState>((set) => ({
  sidebarWidth: 280,
  sidebarCollapsed: false,
  workspaceVisible: true,
  workspaceWidth: 320,

  toggleSidebar: () =>
    set((s) => ({ sidebarCollapsed: !s.sidebarCollapsed })),
  toggleWorkspace: () =>
    set((s) => ({ workspaceVisible: !s.workspaceVisible })),
  setSidebarWidth: (w) => set({ sidebarWidth: w }),
  setWorkspaceWidth: (w) => set({ workspaceWidth: w }),
}));
