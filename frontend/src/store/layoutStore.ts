import { create } from "zustand";

/** 面板宽度约束（px）：两侧面板保持紧凑，把宽度留给中间的聊天主区 */
export const SIDEBAR_MIN = 200;
export const SIDEBAR_MAX = 480;
export const WORKSPACE_MIN = 240;
export const WORKSPACE_MAX = 560;

const SIDEBAR_DEFAULT = 240;
const WORKSPACE_DEFAULT = 280;

const LS_SIDEBAR = "tars.layout.sidebarWidth";
const LS_WORKSPACE = "tars.layout.workspaceWidth";

function clamp(v: number, min: number, max: number): number {
  return Math.min(max, Math.max(min, v));
}

/** 从 localStorage 读取上次的宽度，非法值回退默认 */
function loadWidth(key: string, fallback: number, min: number, max: number): number {
  try {
    const raw = localStorage.getItem(key);
    if (raw === null) return fallback;
    const n = Number(raw);
    if (!Number.isFinite(n)) return fallback;
    return clamp(n, min, max);
  } catch {
    return fallback;
  }
}

function saveWidth(key: string, w: number) {
  try {
    localStorage.setItem(key, String(w));
  } catch {
    // localStorage 不可用时静默忽略（宽退回默认值，不影响功能）
  }
}

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
  /** 设置侧边栏宽度（自动 clamp 并持久化） */
  setSidebarWidth: (w: number) => void;
  /** 设置工作区面板宽度（自动 clamp 并持久化） */
  setWorkspaceWidth: (w: number) => void;
}

export const useLayoutStore = create<LayoutState>((set) => ({
  sidebarWidth: loadWidth(LS_SIDEBAR, SIDEBAR_DEFAULT, SIDEBAR_MIN, SIDEBAR_MAX),
  sidebarCollapsed: false,
  workspaceVisible: true,
  workspaceWidth: loadWidth(LS_WORKSPACE, WORKSPACE_DEFAULT, WORKSPACE_MIN, WORKSPACE_MAX),

  toggleSidebar: () =>
    set((s) => ({ sidebarCollapsed: !s.sidebarCollapsed })),
  toggleWorkspace: () =>
    set((s) => ({ workspaceVisible: !s.workspaceVisible })),
  setSidebarWidth: (w) => {
    const clamped = clamp(w, SIDEBAR_MIN, SIDEBAR_MAX);
    saveWidth(LS_SIDEBAR, clamped);
    set({ sidebarWidth: clamped });
  },
  setWorkspaceWidth: (w) => {
    const clamped = clamp(w, WORKSPACE_MIN, WORKSPACE_MAX);
    saveWidth(LS_WORKSPACE, clamped);
    set({ workspaceWidth: clamped });
  },
}));
