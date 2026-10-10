import type { SessionSummaryView } from "../shared/types";

export interface WorkspaceBucket {
  id: string;
  name: string;
  path: string;
  detail?: string;
  sessions: SessionSummaryView[];
}

export function workspaceName(path: string): string {
  return path.replace(/[\\/]+$/, "").split(/[\\/]/).pop() || path;
}

/** Full paths are identities; equal directory names never merge different workspaces. */
export function workspaceBuckets(sessions: readonly SessionSummaryView[]): WorkspaceBucket[] {
  const byPath = new Map<string, WorkspaceBucket>();
  for (const session of sessions) {
    const path = session.workdir?.trim();
    if (!path) continue;
    let bucket = byPath.get(path);
    if (!bucket) {
      bucket = { id: `workspace:${path}`, name: workspaceName(path), path, sessions: [] };
      byPath.set(path, bucket);
    }
    bucket.sessions.push(session);
  }
  const buckets = [...byPath.values()];
  for (const bucket of buckets) {
    bucket.sessions.sort((a, b) => b.updatedAt.localeCompare(a.updatedAt));
    if (buckets.filter(other => other.name === bucket.name).length > 1) {
      const path = bucket.path.replace(/[\\/]+$/, "");
      bucket.detail = path.slice(0, path.length - bucket.name.length) || bucket.path;
    }
  }
  return buckets.sort((a, b) => b.sessions[0]!.updatedAt.localeCompare(a.sessions[0]!.updatedAt));
}
