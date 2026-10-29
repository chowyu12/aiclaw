import { strict as assert } from "node:assert";
import { test } from "node:test";
import { workspaceBuckets, workspaceName, projectBuckets } from "../apps/desktop/src/renderer/workspace-buckets.ts";
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

test("project identity is independent from shared directories and empty projects remain visible", () => {
 const projects = [{id:"p_one",name:"One",directories:["/shared","/second"]},{id:"p_two",name:"Two",directories:["/shared"]},{id:"p_empty",name:"Empty",directories:[]},{id:"g_old",name:"Old group"}];
 const result=projectBuckets([session("a","/shared","2026-10-29"),session("b","/shared","2026-10-28"),session("legacy","/legacy","2026-10-27")], projects, {a:"p_one",b:"p_two",legacy:"g_old"});
 assert.deepEqual(result.map(bucket=>bucket.id),["p_one","p_two","p_empty","workspace:/legacy"]);
 assert.deepEqual(result[0]!.sessions.map(item=>item.id),["a"]);
 assert.deepEqual(result[1]!.sessions.map(item=>item.id),["b"]);
 assert.deepEqual(result[0]!.directories,["/shared","/second"]);
 assert.equal(result[2]!.path,"");
 assert.deepEqual(result[2]!.sessions,[]);
});
