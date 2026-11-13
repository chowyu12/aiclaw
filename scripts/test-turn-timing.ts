import test from 'node:test';
import assert from 'node:assert/strict';
import { turnTiming } from '../apps/desktop/src/renderer/turn-timing.ts';
import { groupTurns,finalMessage } from '../apps/desktop/src/renderer/turns.ts';
import type { StepEntry } from '../apps/desktop/src/renderer/turns.ts';
const step=(value:Partial<StepEntry>):StepEntry=>({kind:'step',id:'s',step:'tool',title:'tool',detail:'',state:'done',...value});
test('timing partitions nested and overlapping phases without double counting',()=>{
 const result=turnTiming([step({step:'llm',startedAt:0,durationMs:1000,ttftMs:200}),step({startedAt:1000,durationMs:2000}),step({startedAt:1200,durationMs:500}),step({phase:'approval',startedAt:1500,durationMs:1000}),step({phase:'save',startedAt:3100,durationMs:100})]);
 assert.deepEqual(result,{total:3200,totals:{waiting:200,model:800,tool:1000,approval:1000,save:100,other:100}});
 assert.equal(Object.values(result.totals).reduce((a,b)=>a+b),result.total);
 assert.equal(turnTiming([step({durationMs:100})]).total,0);
});
test('save telemetry does not turn a completed answer into a progress message',()=>{
 const turns=groupTurns([{kind:'agent',id:'a',text:'final',streaming:false},step({phase:'save',startedAt:0,durationMs:2})]);
 assert.equal(finalMessage(turns[0]!)?.text,'final');assert.equal(turns[0]!.steps.length,1);
});
