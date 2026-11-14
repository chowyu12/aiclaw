import test from "node:test";
import assert from "node:assert/strict";
import { fetchReleaseNotes } from "../apps/desktop/src/main/release-notes.ts";

test("only published stable releases become bounded notes with trusted links", async () => {
  const fetcher = (async () => Response.json([
    { tag_name: "v3.11.4", published_at: "2026-11-14T00:00:00Z", draft: false, prerelease: false, body: "Notes", html_url: "https://example.com/untrusted" },
    { tag_name: "v3.12.0", published_at: "2026-11-14T00:00:00Z", draft: true, prerelease: false },
    { tag_name: "v3.12.0-rc1", published_at: "2026-11-14T00:00:00Z", draft: false, prerelease: true },
    { tag_name: "../../bad", published_at: "2026-11-14T00:00:00Z", draft: false, prerelease: false },
  ])) as typeof fetch;
  assert.deepEqual(await fetchReleaseNotes(fetcher), [{ tag: "v3.11.4", publishedAt: "2026-11-14T00:00:00Z", body: "Notes", url: "https://github.com/chowyu12/aiclaw/releases/tag/v3.11.4" }]);
});

test("release errors remain errors rather than empty history", async () => {
  await assert.rejects(fetchReleaseNotes((async () => new Response("", { status: 503 })) as typeof fetch), /HTTP 503/);
  await assert.rejects(fetchReleaseNotes((async () => Response.json({ message: "invalid" })) as typeof fetch), /Invalid release response/);
});
