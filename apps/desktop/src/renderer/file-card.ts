export function filename(path: string): string {
  return path.split(/[\\/]/).pop() || path;
}

export function fileType(path: string): string {
  const name = filename(path);
  return name.includes(".") ? name.split(".").pop()?.toUpperCase() || "FILE" : "FILE";
}
