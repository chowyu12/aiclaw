/** Calendar dates follow the desktop's local timezone, matching the usage summary API. */
export function calendarDay(date: Date): string {
  return `${date.getFullYear()}-${String(date.getMonth() + 1).padStart(2, "0")}-${String(date.getDate()).padStart(2, "0")}`;
}
export function sessionCreationDays(sessions: readonly { createdAt: string }[]): { day: string; value: number }[] {
  const counts = new Map<string, number>();
  for (const session of sessions) {
    const date = new Date(session.createdAt);
    if (!Number.isFinite(date.getTime())) continue;
    const key = calendarDay(date);
    counts.set(key, (counts.get(key) ?? 0) + 1);
  }
  return [...counts].map(([day, value]) => ({ day, value }));
}
export function activityCalendar(entries: readonly { day: string; value: number }[], today = new Date(), firstRecordedDay?: string | null) {
  const end = new Date(today.getFullYear(), today.getMonth(), today.getDate(), 12);
  const start = new Date(end); start.setDate(start.getDate() - 364);
  const first = new Date(start); first.setDate(first.getDate() - first.getDay());
  const last = new Date(end); last.setDate(last.getDate() + 6 - last.getDay());
  const counts = new Map(entries.map(entry => [entry.day, Math.max(0, entry.value)]));
  const low = calendarDay(start), high = calendarDay(end);
  const peak = Math.max(1, ...entries.filter(entry => entry.day >= low && entry.day <= high).map(entry => entry.value));
  const cells: { day: string; value: number | null; level: number; inRange: boolean }[] = [];
  const months: { column: number; date: Date }[] = [];
  let previousMonth = -1, total = 0;
  for (const date = new Date(first); date <= last; date.setDate(date.getDate() + 1)) {
    const day = calendarDay(date), inRange = day >= low && day <= high;
    const known = firstRecordedDay !== null && (firstRecordedDay === undefined || day >= firstRecordedDay);
    const value = known ? counts.get(day) ?? 0 : null;
    if (inRange && value !== null) total += value;
    if (date.getDay() === 0 && date.getMonth() !== previousMonth) {
      months.push({ column: Math.floor(cells.length / 7) + 1, date: new Date(date) });
      previousMonth = date.getMonth();
    }
    cells.push({ day, value, level: value ? Math.min(4, Math.ceil(value / peak * 4)) : 0, inRange });
  }
  return { cells, months, total, weeks: cells.length / 7 };
}
