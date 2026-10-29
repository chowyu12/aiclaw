export interface ProjectInput { id?: string; name: string; directories: string[] }

/** Directory order is meaningful: the first directory is the default for new chats. */
export function normalizeProject(input: ProjectInput): ProjectInput {
  if (input?.id !== undefined && (typeof input.id !== "string" || !/^(p_|g_|workspace:)/.test(input.id))) throw new Error("Invalid project identifier");
  if (!input || typeof input.name !== "string" || !input.name.trim()) throw new Error("Project name is required");
  if (input.name.trim().length > 200) throw new Error("Project name is too long");
  if (!Array.isArray(input.directories) || input.directories.some(path => typeof path !== "string")) throw new Error("Invalid project directories");
  const directories = [...new Set(input.directories.map(path => path.trim()).filter(Boolean))];
  if (directories.length > 30) throw new Error("A project supports at most 30 directories");
  return {id: input.id, name: input.name.trim(), directories};
}
