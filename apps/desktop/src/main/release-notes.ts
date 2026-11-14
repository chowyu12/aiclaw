/** Read only published releases from the project's public GitHub API. */
export interface ReleaseNote {
  tag: string;
  publishedAt: string;
  body: string;
  url: string;
}

export async function fetchReleaseNotes(fetcher: typeof fetch = fetch): Promise<ReleaseNote[]> {
  const response = await fetcher("https://api.github.com/repos/chowyu12/aiclaw/releases?per_page=10", {
    signal: AbortSignal.timeout(10_000),
    headers: { Accept: "application/vnd.github+json", "Cache-Control": "no-cache" },
  });
  if (!response.ok) throw new Error(`HTTP ${response.status}`);
  const data: unknown = await response.json();
  if (!Array.isArray(data)) throw new Error("Invalid release response");
  return data.flatMap((value): ReleaseNote[] => {
    if (!value || typeof value !== "object" || value.draft !== false || value.prerelease !== false ||
        typeof value.tag_name !== "string" || !/^v\d+\.\d+\.\d+$/.test(value.tag_name) ||
        typeof value.published_at !== "string") return [];
    return [{
      tag: value.tag_name,
      publishedAt: value.published_at,
      body: typeof value.body === "string" ? value.body.slice(0, 100_000) : "",
      url: `https://github.com/chowyu12/aiclaw/releases/tag/${encodeURIComponent(value.tag_name)}`,
    }];
  }).slice(0, 10);
}
