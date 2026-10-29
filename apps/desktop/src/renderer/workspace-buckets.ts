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

export interface ProjectBucket extends WorkspaceBucket { projectId?: string; directories?: string[] }

/** Explicit membership wins, even when two projects share a directory. */
export function projectBuckets(sessions: readonly SessionSummaryView[], projects: readonly {id:string; name:string; directories?: readonly string[]}[], assignments: Record<string,string>): ProjectBucket[] {
  projects = projects.filter(project => Array.isArray(project.directories));
  const ids = new Set(projects.map(project => project.id));
  const assigned = new Set<string>();
  const buckets: ProjectBucket[] = projects.map(project => {
    const members = sessions.filter(session => assignments[session.id] === project.id);
    members.forEach(session => assigned.add(session.id));
    const directories = [...(project.directories ?? [])];
    return {id:project.id, projectId:project.id, name:project.name, path:directories[0] ?? "", directories, sessions:members.sort((a,b)=>b.updatedAt.localeCompare(a.updatedAt))};
  });
  const loose = sessions.filter(session => !assigned.has(session.id) && !ids.has(assignments[session.id] ?? ""));
  buckets.push(...workspaceBuckets(loose));
  return buckets;
}
