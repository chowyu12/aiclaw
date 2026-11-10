import { test } from "node:test";
import { strict as assert } from "node:assert";
import { activityCalendar, calendarDay, sessionCreationDays } from "../apps/desktop/src/renderer/activity-calendar.ts";

test("calendar covers exactly 365 local dates and pads complete Sunday-first weeks", () => {
  const today = new Date(2026, 10, 10, 1);
  const result = activityCalendar([{ day: "2026-11-10", value: 3 }, { day: "2026-11-11", value: 99 }], today);
  assert.equal(result.cells.filter(cell => cell.inRange).length, 365);
  assert.equal(result.cells.length % 7, 0);
  assert.equal(result.cells.filter(cell => cell.inRange).at(-1)?.day, "2026-11-10");
  assert.equal(result.total, 3, "future data cannot enter the visible-year total");
  assert.ok(result.months.every(month => month.column >= 1 && month.column <= result.weeks));
});
test("session creation counts use real timestamps and ignore invalid dates", () => {
  const stamp = new Date(2026, 10, 10, 12).toISOString();
  assert.deepEqual(sessionCreationDays([{ createdAt: stamp }, { createdAt: stamp }, { createdAt: "invalid" }]), [{ day: "2026-11-10", value: 2 }]);
  assert.equal(calendarDay(new Date(2026, 0, 1, 0, 15)), "2026-01-01");
});
test("usage with no records remains unknown, including dates before collection began", () => {
  const today = new Date(2026, 10, 10);
  const missing = activityCalendar([], today, null);
  assert.ok(missing.cells.filter(cell => cell.inRange).every(cell => cell.value === null));
  const recorded = activityCalendar([{ day: "2026-11-10", value: 15 }], today, "2026-11-09");
  assert.equal(recorded.cells.find(cell => cell.day === "2026-11-08")?.value, null);
  assert.equal(recorded.cells.find(cell => cell.day === "2026-11-09")?.value, 0);
  assert.equal(recorded.cells.find(cell => cell.day === "2026-11-10")?.level, 4);
});
test("leap years and year boundaries do not duplicate or skip calendar dates", () => {
  const result = activityCalendar([], new Date(2024, 2, 1));
  const dates = result.cells.filter(cell => cell.inRange).map(cell => cell.day);
  assert.equal(dates.length, new Set(dates).size);
  assert.ok(dates.includes("2024-02-29"));
});
