/** Launch the actual package in an isolated profile, then inspect its main
 * process over a loopback-only debugger. No production test hooks or UI input.
 */
import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import { mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { resolve, join } from "node:path";
import { setTimeout as pause } from "node:timers/promises";
import { createRequire } from "node:module";

const WebSocket = globalThis.WebSocket ?? createRequire(import.meta.url)("undici").WebSocket;

const executable = resolve(process.argv[2]);
const home = await mkdtemp(join(tmpdir(), "aiclaw-packaged-computer-"));
const pending = new Map(); let socket, child, sequence=0, stderr="";
const evaluate = expression => new Promise((resolve,reject) => {
  const id=++sequence;
  pending.set(id,{resolve,reject});
  socket.send(JSON.stringify({id,method:"Runtime.evaluate",params:{expression,awaitPromise:true,returnByValue:true}}));
});
try {
  child=spawn(executable,["--inspect=127.0.0.1:0",`--user-data-dir=${join(home,"profile")}`,...(process.env.AICLAW_TEST_NO_SANDBOX === "1" ? ["--no-sandbox"] : [])],{
    stdio:["ignore","ignore","pipe"],env:{...process.env,HOME:home,USERPROFILE:home,APPDATA:join(home,"AppData/Roaming"),LOCALAPPDATA:join(home,"AppData/Local")},
  });
  child.on("error",error=>{stderr+=String(error);});
  child.stderr.on("data",chunk=>{stderr=(stderr+String(chunk)).slice(-16000);});
  let endpoint;
  for(let attempt=0;attempt<200;attempt++) {
    endpoint=stderr.match(/Debugger listening on (ws:\/\/127\.0\.0\.1:\d+\/[^\s]+)/)?.[1];
    if(endpoint) break;
    if(child.exitCode!==null) throw Error(`Packaged app exited (${child.exitCode}): ${stderr}`);
    await pause(100);
  }
  assert.ok(endpoint,`Packaged app did not open its local inspector: ${stderr}`);
  socket=new WebSocket(endpoint);
  await new Promise((resolve,reject)=>{
    const timeout=setTimeout(()=>reject(Error(`Packaged inspector connection timed out: ${stderr}`)),5000);
    socket.addEventListener("open",()=>{clearTimeout(timeout);resolve();},{once:true});
    socket.addEventListener("error",error=>{clearTimeout(timeout);reject(error);},{once:true});
  });
  socket.addEventListener("message",event=>{
    const message=JSON.parse(String(event.data)), waiter=pending.get(message.id);
    if(!waiter) return; pending.delete(message.id);
    if(message.error || message.result?.exceptionDetails) waiter.reject(Error(JSON.stringify(message.error??message.result.exceptionDetails)));
    else waiter.resolve(message.result.result?.value);
  });
  socket.addEventListener("close",()=>{for(const {reject} of pending.values()) reject(Error("Packaged inspector closed before verification"));pending.clear();});
  let readiness;
  const watchdog=setTimeout(()=>{
    for(const {reject} of pending.values()) reject(Error("Packaged verification timed out"));
    pending.clear(); socket?.close(); child?.kill();
  },60_000);
  watchdog.unref();
  try {
    for(let attempt=0;attempt<100;attempt++) {
      readiness=await evaluate(`(async()=>{
        const require=process.getBuiltinModule('module').createRequire(process.resourcesPath+'/app/package.json');
        let electron;
        try {electron=require('electron');} catch {return false;}
        const {app,BrowserWindow}=electron;
        if(!app.isReady()) return false;
        const window=BrowserWindow.getAllWindows()[0];
        if(!window||window.webContents.isLoading()) return false;
        return window.webContents.executeJavaScript('Boolean(window.aiclaw?.plugins && document.querySelector("#app")?.children.length)');
      })()`);
      if(readiness) break;
      await pause(100);
    }
    assert.equal(readiness,true,"Packaged renderer and preload must load");
    const receipt=await evaluate(`(async()=>{
      const require=process.getBuiltinModule('module').createRequire(process.resourcesPath+'/app/package.json');
      const {createComputerRuntime}=require(process.resourcesPath+'/app/dist/main/computer-runtime.js');
      const runtime=await createComputerRuntime();
      try {
        const result=await runtime.call('permission_status',{});
        if(result.isError) throw Error(JSON.stringify(result));
        const status=JSON.parse(result.content[0].text);
        return {tools:runtime.tools.length,status,resources:process.resourcesPath};
      } finally {await runtime.close();}
    })()`);
    assert.equal(receipt.tools,18);
    assert.ok(receipt.status.runtime_directory?.startsWith(join(home,".aiclaw/computer-use")),"Packaged runtime must execute from the isolated user installation");
    console.log(JSON.stringify({passed:true,packaged_executable:executable,renderer_and_preload:true,shipped_modules_loaded:true,user_runtime_installed:true,source_identity_handshake:true,...receipt}));
  } finally {clearTimeout(watchdog);}
} catch(error) {
  throw Error(`${String(error)}\nPackaged stderr:\n${stderr}`);
} finally {
  socket?.close();
  if(child && child.exitCode===null) {
    child.kill(); await pause(1000);
    if(child.exitCode===null) {child.kill("SIGKILL");await pause(100);}
  }
  child?.stderr.destroy(); child?.unref();
  await rm(home,{recursive:true,force:true,maxRetries:10,retryDelay:200});
}
