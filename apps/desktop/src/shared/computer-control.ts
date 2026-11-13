/** Desktop control state is owned by the host and shared across every chat. */
export interface ComputerControlActivity {
  sessionId: string;
  turnId: string;
  action: string;
  foreground?: boolean;
  app: string;
  displayName?: string;
  windowId?: number;
  pid?: number;
  frame?: { x: number; y: number; width: number; height: number };
}
export interface ComputerControlState {
  mode: "ready" | "paused" | "stopped";
  active: ComputerControlActivity | null;
  watching: number;
}
export interface ComputerDiagnostics {
  platform: string;
  available: boolean;
  accessibility: boolean | null;
  screenCapture: boolean | null;
  toolCount: number;
  message: string;
}
export type ComputerControlAction = "pause" | "resume" | "stop";

const observationActions = new Set(["get_window_state", "list_windows", "verify_state", "zoom", "check_permissions", "list_apps", "find_apps", "frontmost_window", "permission_status", "get_app_state", "query", "watch_start", "watch_next", "watch_stop", "app_policy"]);
export function computerActionIsObservation(action: string): boolean { return observationActions.has(action); }
