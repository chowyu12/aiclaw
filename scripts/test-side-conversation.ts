import test from 'node:test';
import {toRaw} from 'vue';
import {applyAgentEvent,type LiveSession} from '../apps/desktop/src/renderer/live-sessions.ts';
import assert from 'node:assert/strict';
import { actions,store } from '../apps/desktop/src/renderer/store.ts';
import { draftFor } from '../apps/desktop/src/renderer/drafts.ts';
test('side restore, send and stop target only the selected session and ignore stale restores',async()=>{
 const sent:any[]=[],stopped:string[]=[];
 let release!:(value:unknown)=>void;
 const info=(id:string)=>({sessionId:id,model:'fixture',providerId:1,workspace:'/fixture',tools:[],mcpStatus:{},history:[{id:'m-'+id,kind:'agentMessage',text:id}]});
 const old=(globalThis as any).window;
 (globalThis as any).window={aiclaw:{session:{
  resume:async(id:string)=>id==='slow'?new Promise(resolve=>{release=resolve}):info(id),
  history:async()=>[],work:async()=>({pending:[],queuedRequestIds:[]}),
  send:async(input:unknown)=>{sent.push(input)},interrupt:async(id:string)=>{stopped.push(id)},
 }}};
 try {
  await actions.openSession('main');
  const pending=actions.openSideSession('slow');
  await actions.openSideSession('side');release(info('slow'));await pending;
  assert.equal(store.sessionId,'main');assert.equal(store.sideSessionId,'side');assert.equal(store.model,'fixture');
  assert.equal(store.live.side?.timeline[0]?.kind,'agent');
  draftFor('main').text='main draft';draftFor('side').text='side draft';
  assert.equal(await actions.send('side message',[],[],[],'side'),true);
  assert.equal(sent[0].sessionId,'side');assert.equal(store.live.side?.busy,true);assert.equal(store.live.main?.busy,false);
  await actions.interrupt('side');assert.deepEqual(stopped,['side']);
  assert.equal(draftFor('main').text,'main draft');
  actions.closeSideSession();assert.equal(store.sideSessionId,'');assert.equal(store.live.side?.busy,true);
  assert.equal(await actions.send('closed',[],[],[],'side'),false);
 } finally {(globalThis as any).window=old;}
});

test('deleting or archiving a side session cancels stale restore and disables further sends',async()=>{
 const old=(globalThis as any).window;let release!:(value:unknown)=>void;const sent:unknown[]=[];const activations:boolean[]=[];
 const info=(id:string)=>({sessionId:id,model:'fixture',providerId:1,tools:[],mcpStatus:{},history:[]});
 (globalThis as any).window={aiclaw:{session:{
  resume:async(id:string,_model:unknown,activate:boolean)=>{activations.push(activate);return id==='slow-removed'?new Promise(resolve=>{release=resolve}):info(id)},
  work:async()=>({pending:[]}),remove:async(id:string)=>[id],archive:async()=>['side-parent','side-child'],list:async()=>[],send:async(input:unknown)=>sent.push(input),
 },groups:{read:async()=>({groups:[],assignments:{},collapsed:{}})}}};
 try {
  const pending=actions.openSideSession('slow-removed');await actions.deleteSession('slow-removed');release(info('slow-removed'));await pending;
  assert.equal(store.sideSessionId,'');assert.equal(store.sideSessionReady,false);assert.equal(await actions.send('hello',[],[],[],'slow-removed'),false);
  await actions.openSideSession('side-child');await actions.archiveSession('side-parent');
  assert.equal(store.sideSessionId,'');assert.equal(store.sideSessionReady,false);assert.deepEqual(sent,[]);assert.ok(activations.every(value=>value===false));
 } finally {(globalThis as any).window=old;}
});

test('side RPC completion cannot overwrite a turn that finished during history restore',async()=>{
 const old=(globalThis as any).window;let release!:(value:unknown)=>void;
 (globalThis as any).window={aiclaw:{session:{resume:async()=>new Promise(resolve=>{release=resolve}),work:async()=>({pending:[]})}}};
 try {
  const restoring=actions.openSideSession('finish-during-restore');
  const live=toRaw(store.live) as Record<string,LiveSession>;
  const event=(method:string,extra:Record<string,unknown>={})=>({method,params:{sessionId:'finish-during-restore',turnId:'t',...extra}});
  applyAgentEvent(live,event('turn/started'),'main');
  applyAgentEvent(live,event('item/completed',{item:{id:'new-answer',kind:'agentMessage',text:'completed while loading'}}),'main');
  applyAgentEvent(live,event('turn/completed'),'main');
  release({sessionId:'finish-during-restore',model:'fixture',providerId:1,history:[{id:'old-answer',kind:'agentMessage',text:'old'}]});await restoring;
  assert.deepEqual(store.live['finish-during-restore']?.timeline.map(item=>item.id),['old-answer','new-answer']);
  assert.equal(store.live['finish-during-restore']?.busy,false);
 } finally {actions.closeSideSession();(globalThis as any).window=old;}
});
