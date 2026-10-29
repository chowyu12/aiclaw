import { homedir } from "node:os";
import { join } from "node:path";

/** Use a dedicated working directory when the user has not chosen one. */
export function resolveWorkspace(workspace?: string): string {
  return workspace?.trim() || join(homedir(), "aiclaw");
}

export function projectWorkspace(project: {directories?: readonly string[]} | undefined, chosen?: string): string {
  const directories = project?.directories ?? [];
  const workspace = chosen?.trim();
  if (project && workspace && !directories.includes(workspace)) throw new Error("Choose a directory configured for this project");
  return resolveWorkspace(workspace || directories[0]);
}
