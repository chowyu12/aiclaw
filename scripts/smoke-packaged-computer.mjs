/** Launch the actual package in an isolated profile, then inspect its main
 * process over a loopback-only debugger. No production test hooks or UI input.
 */
import assert from "node:assert/strict";
import { execFileSync, spawn } from "node:child_process";
import { mkdtemp, realpath, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { resolve, join } from "node:path";
import { setTimeout as pause } from "node:timers/promises";
import { createRequire } from "node:module";

const WebSocket = globalThis.WebSocket ?? createRequire(import.meta.url)("undici").WebSocket;

const executable = resolve(process.argv[2]);
// macOS Unix sockets allow only 103 path bytes; os.tmpdir() can itself be long.
const home = await mkdtemp(join(process.platform === "darwin" ? "/tmp" : tmpdir(), "aiclaw-pkg-"));
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
        if(!process.resourcesPath) return false;
        const require=process.getBuiltinModule('module').createRequire(process.resourcesPath+'/app/package.json');
        let electron;
        try {electron=require('electron');} catch {return false;}
        const {app,BrowserWindow}=electron;
        if(!app?.isReady?.()) return false;
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
      const path=require('node:path'),fs=require('node:fs');
      const root=path.join(process.resourcesPath,'app/cua-driver');
      if(fs.existsSync(path.join(root,'CuaDriver.app')))throw Error('An independent app must not be bundled');
      const {createComputerRuntime}=require(process.resourcesPath+'/app/dist/main/computer-runtime.js');
      const runtime=await createComputerRuntime();
      try {
        const result=await runtime.call('check_permissions',{prompt:false});
        if(result.isError)throw Error(JSON.stringify(result));
        const status=result.structuredContent;
        if(process.platform==='darwin'&&(status?.source?.attribution!=='host'||status.source.embedded!==true))throw Error('Expected host-owned TCC permissions');
        const identity=await runtime.call('health_report',{include:['bundle_identity']});
        return {tools:runtime.tools.length,status,identity:identity.structuredContent,resources:process.resourcesPath};
      }finally{await runtime.close();}
    })()`);
    assert.ok(receipt.tools>=17);
    if(process.platform==='darwin'){
      assert.equal(receipt.status.source.host_bundle_id,'com.github.chowyu12.aiclaw');
      const identity=receipt.identity.checks.find(check=>check.name==='bundle_identity');
      assert.equal(identity.status,'pass');
      assert.equal(identity.data.bundle_identifier,'com.github.chowyu12.aiclaw');
      assert.equal(identity.data.parent_process_id,receipt.status.source.responsible_ppid);
    }
    if(process.env.AICLAW_CUA_BACKGROUND_TEST==="1") {
      const {backgroundFixtureChecks}=await import("./cua-background-fixture.mjs");
      console.log(JSON.stringify(await backgroundFixtureChecks(evaluate)));
    }
    if(process.env.AICLAW_CUA_TEXTEDIT_TEST==="1") {
      const {textEditChecks}=await import("./cua-textedit-fixture.mjs");
      const result=await textEditChecks(evaluate);
      console.log(JSON.stringify({textedit_fixture:result}));
      if(result.error||result.save_error||!result.save_panel) throw Error("TextEdit background save test did not complete");
    }
    let mainWindowCloseVerified = false;
    if (process.platform !== "darwin") {
      clearTimeout(watchdog);
      // A real hidden auxiliary BrowserWindow reproduces the window-count edge
      // case without dispatching desktop input or creating production test hooks.
      const closing = await evaluate(`(()=>{
        const require=process.getBuiltinModule('module').createRequire(process.resourcesPath+'/app/package.json');
        const {BrowserWindow}=require('electron');
        const main=BrowserWindow.getAllWindows().find(window=>window.webContents.getURL().includes('/renderer/index.html'));
        if(!main) throw Error('Packaged main window is missing');
        const hidden=new BrowserWindow({show:false,width:1,height:1,title:'AIClaw hidden lifecycle fixture',webPreferences:{sandbox:true,contextIsolation:true,nodeIntegration:false}});
        globalThis.__aiclawHiddenLifecycleFixture=hidden;
        const verified=!hidden.isVisible() && BrowserWindow.getAllWindows().length>=2;
        setTimeout(()=>main.close(),100);
        return {hiddenAuxiliaryWindow:verified};
      })()`);
      assert.equal(closing.hiddenAuxiliaryWindow,true,"A hidden auxiliary window must be present during close verification");
      // Detach the debugger so it cannot itself hold Node alive after close.
      socket.close();
      for(let attempt=0;attempt<150 && child.exitCode===null && child.signalCode===null;attempt++) await pause(100);
      assert.equal(child.signalCode,null,"Closing the main window must exit naturally without a termination signal");
      assert.equal(child.exitCode,0,"Closing the main window must exit even when a hidden control window exists");
      mainWindowCloseVerified=true;
    }
    console.log(JSON.stringify({passed:true,packaged_executable:executable,renderer_and_preload:true,shipped_modules_loaded:true,embedded_host_permission_identity:true,...(mainWindowCloseVerified?{main_window_close_exits_naturally:true,hidden_auxiliary_does_not_prevent_exit:true}:{}),...receipt}));
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
