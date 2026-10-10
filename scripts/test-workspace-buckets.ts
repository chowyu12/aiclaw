import { strict as assert } from "node:assert";
import { test } from "node:test";
import { workspaceBuckets, workspaceName } from "../apps/desktop/src/renderer/workspace-buckets.ts";
import type { SessionSummaryView } from "../apps/desktop/src/shared/types.ts";

const session = (id: string, workdir: string, updatedAt: string): SessionSummaryView => ({
  id, workdir, updatedAt, createdAt: updatedAt, title: id, turnCount: 1, model: "test",
});

test("workspaces keep full-path identity and disambiguate equal directory names", () => {
  const buckets = workspaceBuckets([
    session("a", "/one/project", "2026-10-09"),
    session("b", "/two/project", "2026-10-10"),
    session("c", "/one/project", "2026-10-08"),
    session("general", "", "2026-10-10"),
  ]);
  assert.equal(buckets.length, 2);
  assert.deepEqual(buckets.map(bucket => bucket.path), ["/two/project", "/one/project"]);
  assert.deepEqual(buckets.map(bucket => bucket.detail), ["/two/", "/one/"]);
  assert.deepEqual(buckets[1]!.sessions.map(session => session.id), ["a", "c"]);
  assert.notEqual(buckets[0]!.id, buckets[1]!.id);
});

test("workspace names support Windows paths and roots without changing stored paths", () => {
  assert.equal(workspaceName("C:\\Users\\me\\project"), "project");
  assert.equal(workspaceName("/"), "/");
  const buckets = workspaceBuckets([session("a", "/project", "2026-10-10")]);
  assert.equal(buckets[0]!.detail, undefined);
  assert.equal(buckets[0]!.path, "/project");
});
