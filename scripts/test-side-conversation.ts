import test from 'node:test';
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
