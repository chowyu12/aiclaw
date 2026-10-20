import { strict as assert } from "node:assert";
import { test } from "node:test";
import { mkdtempSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import type { AppConfigRequest, PendingAppConfigAction } from "../packages/agent-client/src/index.ts";
import { ConfigurationAssistant } from "../apps/desktop/src/main/app-config.ts";
import { Scheduler } from "../apps/desktop/src/main/scheduler.ts";
import type { ConfigurationCard } from "../apps/desktop/src/shared/app-config.ts";

type Host = ConstructorParameters<typeof ConfigurationAssistant>[0];
function fixture(t: import("node:test").TestContext) {
  const dir = mkdtempSync(join(tmpdir(),"aiclaw-config-"));
  t.after(() => rmSync(dir,{recursive:true,force:true}));
  const engines = [{id:1,name:"Existing",provider:"tavily",enabled:true,apiKeySet:true,baseUrl:"https://example.com/?key=private"}];
  const plugins = [{uuid:"email",pluginId:"aiclaw.email",name:"Mail",enabled:false,source:"builtin",missingConfig:[],permissions:["network.access"],channels:0,connections:[]}];
  const calls: unknown[][] = [];
  let servers: any[] = [{id:"m1",label:"Remote",transport:"http",url:"https://example.com/private?token=secret",enabled:false,headers:{Authorization:"SECRET"}}];
  let cards: ConfigurationCard[] = [];
  let failSearch = false, probeOK = true, mailOK = true;
  let duringProbe = () => {};
  const sessions = {
    listPlugins:async()=>plugins,
    pluginConfig:async()=>[{key:"address",description:"Address",secret:false,value:"user@example.com",required:true,isSet:true},{key:"password",description:"Password",secret:true,required:true,isSet:true}],
    setPluginConfig:async(...args:unknown[])=>{calls.push(["setPlugin",...args]);},
    togglePlugin:async(...args:unknown[])=>{calls.push(["togglePlugin",...args]);},
    deletePlugin:async()=>{}, installPlugin:async(path:string)=>{calls.push(["install",path]);},
    emailTest:async()=>({ok:mailOK}),
    listSearchEngines:async()=>engines,
    createSearchEngine:async(p:any)=>{const engine={...p,id:engines.length+1,apiKeySet:!!p.apiKey};engines.push(engine);calls.push(["createSearch",p]);return engine;},
    updateSearchEngine:async(p:any)=>{const engine=engines.find(e=>e.id===p.id)!;Object.assign(engine,p,p.apiKey?{apiKeySet:true}:{});calls.push(["updateSearch",p]);return engine;},
    deleteSearchEngine:async(id:number)=>{calls.push(["deleteSearch",id]);},
    testSearchEngine:async(id:number)=>{calls.push(["testSearch",id]);if(failSearch)throw Error("Authorization: SECRET");return {results:[]};},
    probeMcp:async(p:unknown)=>{calls.push(["probe",p]);duringProbe();return {ok:probeOK,tools:[]};},
    mcpOAuthLogin:async(p:unknown)=>{calls.push(["login",p]);return {state:"authorizing"};},
    mcpOAuthStatus:async()=>({state:"needs_login"}),
  } as unknown as Host;
  const scheduler = new Scheduler(dir,{runTask:async()=>"s",notify:()=>{}});
  const assistant = new ConfigurationAssistant(sessions,{readMcpServers:()=>structuredClone(servers),writeMcpServers:value=>{servers=structuredClone(value);return value;}},scheduler,value=>{cards=value;});
  const results:string[] = [], failures:string[] = [];
  const request = async (category:AppConfigRequest["category"], operation?:string, targetId?:string, values?:Record<string,unknown>, sessionId="s") => {
    const action:PendingAppConfigAction = {request:{sessionId,action:operation?"configure":"list",category,operation,targetId,values},respond:r=>results.push(r.text),fail:error=>failures.push(error)};
    await assistant.request(action);
    return cards.at(-1);
  };
  const apply = async (changes:Record<string,string|boolean> = {}, card=cards.at(-1)!) => assistant.respond({id:card.id,values:{...Object.fromEntries(card.fields.map(f=>[f.key,f.value])),...changes}});
  return {assistant,request,apply,results,failures,calls,engines,scheduler,plugins,cards:()=>cards,servers:()=>servers,duringProbe:(action:()=>void)=>duringProbe=action,setProbe:(ok:boolean)=>probeOK=ok,setSearchFail:()=>failSearch=true,setMail:(ok:boolean)=>mailOK=ok};
}

test("status returns useful IDs but no credentials, URLs or raw MCP config",async t=>{
  const f=fixture(t);
  for(const category of ["email","search","mcp","plugin","schedule"] as const)await f.request(category);
  const result=f.results.join("\n");
  assert.match(result,/m1/);assert.match(result,/apiKeySet/);assert.doesNotMatch(result,/SECRET|private|Authorization|https:/);
  assert.equal(f.cards().length,0);assert.equal(f.calls.length,0);
});

test("model proposals cannot include credentials or unsupported operations",async t=>{
  const f=fixture(t);
  await f.request("search","save",undefined,{apiKey:"SECRET"});
  await f.request("mcp","save",undefined,{env:{TOKEN:"SECRET"}});
  await f.request("plugin","install",undefined,{path:"/malicious"});
  await f.request("email","delete","email");
  assert.equal(f.failures.length,4);assert.equal(f.cards().length,0);assert.equal(f.calls.length,0);
  assert.doesNotMatch(f.failures.join(""),/SECRET|malicious/);
});

test("email edits wait for local confirmation, preserve existing secret and test before enabling",async t=>{
  const f=fixture(t);await f.request("email","save",undefined,{address:"changed@example.com"});
  assert.equal(f.calls.length,0);assert.equal(f.results.length,0);
  assert.equal(f.cards()[0]!.fields.find(field=>field.secret)!.value,"");
  await f.apply();
  assert.ok(f.calls.some(call=>call[0]==="setPlugin"&&call[2]==="address"));
  assert.ok(!f.calls.some(call=>call[0]==="setPlugin"&&call[2]==="password"));
  assert.deepEqual(f.calls.at(-1),["togglePlugin","email",true]);
  assert.match(f.results[0]!,/test passed/);
});

test("email test failure does not newly enable the plugin",async t=>{
  const f=fixture(t);f.setMail(false);await f.request("email","save");await f.apply({password:"LOCAL_SECRET"});
  assert.ok(!f.calls.some(call=>call[0]==="togglePlugin"));assert.match(f.results[0]!,/test failed/);
  assert.doesNotMatch(f.results.join(""),/LOCAL_SECRET/);
});

test("cancelled and expired requests cannot write, including after reload",async t=>{
  const f=fixture(t);const card=await f.request("email","save");
  assert.equal(f.assistant.listPending()[0]!.id,card!.id);
  await f.assistant.respond({id:card!.id,cancel:true});
  await assert.rejects(f.apply({},card));assert.equal(f.calls.length,0);
  await f.request("search","save");f.assistant.clear("s");
  assert.equal(f.cards().length,0);assert.equal(f.calls.length,0);
});

test("successful default search selection tests first, then updates enabled engines",async t=>{
  const f=fixture(t);await f.request("search","default",undefined,{name:"New",provider:"serpapi"});
  await f.apply({apiKey:"LOCAL_SECRET"});
  assert.equal(f.engines[0]!.enabled,false);assert.equal(f.engines[1]!.enabled,true);
  assert.ok(f.calls.findIndex(c=>c[0]==="testSearch")<f.calls.findIndex(c=>c[0]==="updateSearch"));
  assert.doesNotMatch(f.results.join(""),/LOCAL_SECRET/);
});

test("failed search test leaves existing default enabled and strips provider errors",async t=>{
  const f=fixture(t);f.setSearchFail();await f.request("search","default",undefined,{name:"New"});
  await assert.rejects(f.apply({apiKey:"LOCAL_SECRET"}),error=>!String(error).includes("SECRET"));
  assert.equal(f.engines[0]!.enabled,true);assert.equal(f.cards().length,1);
  await assert.rejects(f.apply(),error=>!String(error).includes("SECRET"));
  assert.equal(f.engines.length,2,"retry updates the same saved engine instead of duplicating it");
});

test("MCP import is local, validated and tested before saving, preserving stored credentials",async t=>{
  const f=fixture(t);await f.request("mcp","save","m1",{label:"Updated"});await f.apply();
  assert.equal(f.servers()[0]!.headers.Authorization,"SECRET");
  assert.equal(f.servers()[0]!.label,"Updated");
  await f.request("mcp","save");await f.apply({import:JSON.stringify({mcpServers:{local:{command:"node",args:["server.js"],env:{TOKEN:"LOCAL_SECRET"}}}})});
  assert.equal(f.servers().length,2);assert.equal(f.servers()[1]!.command,"node");assert.equal(f.servers()[1]!.enabled,false);
  assert.doesNotMatch(f.results.join(""),/LOCAL_SECRET|Authorization/);
});

test("failed MCP probes do not replace existing configuration; OAuth can save disabled for login",async t=>{
  const f=fixture(t);f.setProbe(false);await f.request("mcp","save","m1",{label:"Changed"});await f.apply();
  assert.equal(f.servers()[0]!.label,"Remote");
  await f.request("mcp","save",undefined,{label:"OAuth",url:"https://example.com/mcp",oauth:true});await f.apply();
  assert.equal(f.servers().length,2);assert.equal(f.servers()[1]!.enabled,false);
  await f.request("mcp","login",f.servers()[1]!.id);await f.apply();assert.match(f.results.at(-1)!,/authorizing/);
});

test("MCP card refuses stale data instead of overwriting settings changed elsewhere",async t=>{
  const f=fixture(t);await f.request("mcp","save","m1");f.servers()[0]!.label="Changed elsewhere";
  await assert.rejects(f.apply());assert.equal(f.servers()[0]!.label,"Changed elsewhere");
});

test("plugin enable, disable, local installation and built-in deletion rules use existing APIs",async t=>{
  const f=fixture(t);
  await f.request("plugin","enable","email");await f.apply();
  await f.request("plugin","disable","email");await f.apply();
  await f.request("plugin","delete","email");assert.equal(f.failures.length,1);
  await f.request("plugin","install");assert.ok(!f.calls.some(c=>c[0]==="install"));await f.apply({path:"/tmp/public-plugin"});
  assert.ok(f.calls.some(c=>c[0]==="install"));
});

test("schedule cards create, edit, pause, resume and delete in the real local scheduler",async t=>{
  const f=fixture(t);await f.request("schedule","save",undefined,{name:"Daily",prompt:"Summarize public news",time:"08:00"});await f.apply();
  const task=f.scheduler.list()[0]!;assert.equal(task.rule.time,"08:00");assert.equal(task.sessionId,"s");
  await f.request("schedule","save",task.id,{time:"09:00"});await f.apply();assert.equal(f.scheduler.list()[0]!.rule.time,"09:00");
  await f.request("schedule","disable",task.id);await f.apply();assert.equal(f.scheduler.list()[0]!.enabled,false);
  await f.request("schedule","enable",task.id);await f.apply();assert.equal(f.scheduler.list()[0]!.enabled,true);
  await f.request("schedule","delete",task.id);await f.apply();assert.equal(f.scheduler.list().length,0);
});

test("extra form fields and double confirmation cannot bypass the pending card",async t=>{
  const f=fixture(t);await f.request("email","save");await assert.rejects(f.apply({unknown:"x"}));assert.equal(f.calls.length,0);
  const card=f.cards()[0]!;await f.apply();await assert.rejects(f.apply({},card));
});


test("MCP save preserves other records changed during a connection test",async t=>{
  const f=fixture(t);await f.request("mcp","save","m1",{label:"Updated"});
  f.duringProbe(()=>{f.servers().push({id:"added",label:"Added elsewhere",transport:"http",url:"https://example.com/new",enabled:false});});
  await f.apply();assert.equal(f.servers().length,2);assert.equal(f.servers()[1]!.id,"added");
});

test("channel plugin configuration targets the selected existing connection",async t=>{
  const f=fixture(t);Object.assign(f.plugins[0]!,{channels:1,connections:[{uuid:"c1",name:"First",missingConfig:[]},{uuid:"c2",name:"Second",missingConfig:[]}]});
  await f.request("plugin","save","email",{connectionId:"c2"});await f.apply({password:"LOCAL_SECRET"});
  assert.ok(f.calls.filter(c=>c[0]==="setPlugin").every(c=>c[4]==="c2"));
  assert.doesNotMatch(f.results.join(""),/LOCAL_SECRET/);
});

test("expired cards cannot apply credentials even if the renderer still holds an old card",async t=>{
  const f=fixture(t);const card=await f.request("email","save");const later=Date.now()+30*60_000;
  t.mock.method(Date,"now",()=>later);
  await assert.rejects(f.apply({password:"LOCAL_SECRET"},card));assert.equal(f.calls.length,0);
});
