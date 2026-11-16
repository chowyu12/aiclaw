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
  (toRaw(store.live) as Record<string,LiveSession>).side!.turnId='side-turn';
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

test('side tabs reuse live history and restore only still-existing persisted tabs',async()=>{
 const old=(globalThis as any).window,oldStorage=(globalThis as any).localStorage;
 const values=new Map<string,string>();let resumes=0;
 (globalThis as any).localStorage={getItem:(key:string)=>values.get(key)??null,setItem:(key:string,value:string)=>values.set(key,value)};
 (globalThis as any).window={aiclaw:{session:{resume:async(id:string)=>{resumes++;return {sessionId:id,history:[]}},work:async()=>({pending:[]})}}};
 try {
  await actions.openSideSession('tab-one');await actions.openSideSession('tab-two');await actions.openSideSession('tab-one');
  assert.equal(resumes,2);assert.equal(store.sideSessionId,'tab-one');
  assert.ok(store.sideSessionIds.includes('tab-one'));assert.ok(store.sideSessionIds.includes('tab-two'));
  const persisted=JSON.parse(values.get('aiclaw.side-sessions')!);assert.equal(persisted.active,'tab-one');
  (toRaw(store) as any).sessions=[{id:'tab-one',title:'one'}];
  values.set('aiclaw.side-sessions',JSON.stringify({ids:['tab-one','deleted',null,'tab-one'],active:'tab-one'}));
  actions.closeSideSession();values.set('aiclaw.side-sessions',JSON.stringify({ids:['tab-one','deleted',null,'tab-one'],active:'tab-one'}));
  await actions.restoreSideSessions();assert.deepEqual([...store.sideSessionIds],['tab-one']);assert.equal(store.sideSessionId,'tab-one');assert.equal(resumes,2);
  actions.closeSideTab('tab-one');assert.equal(store.sideSessionId,'');assert.equal(store.sideSessionIds.length,0);
  values.set('aiclaw.side-sessions','{broken');await actions.restoreSideSessions();assert.equal(store.sideSessionId,'');
 } finally {actions.closeSideSession();(globalThis as any).window=old;(globalThis as any).localStorage=oldStorage;}
});

test('UI stop carries the observed turn id and never falls back to a session-wide stop',async()=>{
 const old=(globalThis as any).window;const stopped:unknown[]=[];
 (globalThis as any).window={aiclaw:{session:{interrupt:async(...args:unknown[])=>stopped.push(args)}}};
 try {
  (toRaw(store.live) as Record<string,LiveSession>)['stop-fixture']={timeline:[],busy:true};await actions.interrupt('stop-fixture');assert.deepEqual(stopped,[]);
  (toRaw(store.live) as Record<string,LiveSession>)['stop-fixture']!.turnId='observed';await actions.interrupt('stop-fixture');assert.deepEqual(stopped,[['stop-fixture','observed']]);
 } finally {(globalThis as any).window=old;}
});

test('a failed startup session list does not erase the saved side layout',async()=>{
 const old=(globalThis as any).window,oldStorage=(globalThis as any).localStorage;
 const saved=JSON.stringify({ids:['saved-tab'],active:'saved-tab'});let value=saved;
 (globalThis as any).localStorage={getItem:()=>value,setItem:(_key:string,next:string)=>{value=next}};
 (globalThis as any).window={aiclaw:{runtime:{start:async()=>{}},session:{list:async()=>{throw Error('temporarily unavailable')}},providers:{list:async()=>[]}}};
 try {await actions.startRuntime();assert.equal(value,saved);}finally{(globalThis as any).window=old;(globalThis as any).localStorage=oldStorage;}
});
