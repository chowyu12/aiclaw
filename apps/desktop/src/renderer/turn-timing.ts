import type { StepEntry } from './turns';
export type TimingPhase = 'waiting' | 'model' | 'tool' | 'approval' | 'save' | 'other';
/** Partition recorded wall time. Nested/parallel steps and approval waits never add twice. */
export function turnTiming(steps: readonly StepEntry[]) {
  const intervals: { start: number; end: number; phase: TimingPhase }[] = [];
  for (const step of steps) {
    if (step.startedAt === undefined || step.durationMs === undefined || !Number.isFinite(step.startedAt) || !Number.isFinite(step.durationMs) || step.durationMs <= 0) continue;
    const start=step.startedAt, end=start+step.durationMs;
    if (step.phase==='approval' || step.phase==='save') intervals.push({start,end,phase:step.phase});
    else if (step.step==='tool') intervals.push({start,end,phase:'tool'});
    else if (step.step==='llm') {
      const first=start+Math.min(step.durationMs,Math.max(0,step.ttftMs??0));
      if(first>start) intervals.push({start,end:first,phase:'waiting'});
      intervals.push({start:first,end,phase:'model'});
    }
  }
  const points=[...new Set(intervals.flatMap(i=>[i.start,i.end]))].sort((a,b)=>a-b);
  const totals:Record<TimingPhase,number>={waiting:0,model:0,tool:0,approval:0,save:0,other:0};
  const priority:TimingPhase[]=['save','approval','tool','waiting','model'];
  for(let n=1;n<points.length;n++) {
    const start=points[n-1]!,end=points[n]!;
    const phase=priority.find(p=>intervals.some(i=>i.phase===p&&i.start<=start&&i.end>=end))??'other';
    totals[phase]+=end-start;
  }
  return {totals,total:points.length?points.at(-1)!-points[0]!:0};
}
