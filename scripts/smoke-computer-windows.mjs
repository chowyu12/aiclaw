/** Windows-only real native acceptance. Only temporary WinForms apps receive input. */
import assert from "node:assert/strict";
import { spawn, execFile } from "node:child_process";
import { mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import { tmpdir, homedir } from "node:os";
import { dirname, join, resolve } from "node:path";
import { setTimeout as pause } from "node:timers/promises";
import { fileURLToPath } from "node:url";
import { promisify } from "node:util";
if (process.platform !== "win32") throw Error("This acceptance requires Windows");
const run = promisify(execFile);
const repo = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const directory = join(repo, "tools/computer-use/windows");
const powershell = join(process.env.SystemRoot, "System32/WindowsPowerShell/v1.0/powershell.exe");
const temporary = await mkdtemp(join(tmpdir(), "aiclaw-windows-acceptance-"));
const children = []; let runtime, shortcutFolder, launchedPID;
const encoded = source => Buffer.from(source, "utf16le").toString("base64");
const { ComputerPortableRuntime } = await import(new URL("../apps/desktop/dist/main/computer-portable-runtime.js", import.meta.url));
const { ComputerController } = await import(new URL("../apps/desktop/dist/main/computer.js", import.meta.url));
const controller = new ComputerController();
const { stageComputerRuntime } = await import("./stage-computer-runtime.mjs");
const value = result => JSON.parse(result.content.find(item => item.type === "text").text.split("\n")[0]);
try {
  const fixture = join(temporary,"AIClawFixture.exe");
  await run(powershell,["-NoProfile","-NonInteractive","-EncodedCommand",encoded('Add-Type -TypeDefinition ([IO.File]::ReadAllText($env:AICLAW_FIXTURE_SOURCE)) -ReferencedAssemblies System.dll,System.Drawing.dll,System.Windows.Forms.dll,System.Web.Extensions.dll -OutputAssembly $env:AICLAW_FIXTURE_EXE -OutputType WindowsApplication')],{env:{...process.env,AICLAW_FIXTURE_SOURCE:join(directory,"Fixture.cs"),AICLAW_FIXTURE_EXE:fixture},timeout:60_000});
  const shortcut=await run(powershell,["-NoProfile","-NonInteractive","-EncodedCommand",encoded('[Console]::OutputEncoding=New-Object Text.UTF8Encoding($false); $folder=Join-Path ([Environment]::GetFolderPath("Programs")) ("AIClaw Acceptance "+[Guid]::NewGuid()); [void](New-Item -ItemType Directory -Path $folder); $shell=New-Object -ComObject WScript.Shell; $link=$shell.CreateShortcut((Join-Path $folder "AIClaw Launch Fixture.lnk")); $link.TargetPath=$env:AICLAW_FIXTURE_EXE; $link.Arguments=\'"\'+$env:AICLAW_LAUNCH_RECEIPT+\'" "AIClaw Launch Fixture"\'; $link.WorkingDirectory=[IO.Path]::GetDirectoryName($env:AICLAW_FIXTURE_EXE); $link.Save(); [Console]::Write($folder)')],{env:{...process.env,AICLAW_FIXTURE_EXE:fixture,AICLAW_LAUNCH_RECEIPT:join(temporary,"launched.json")},timeout:30_000});
  shortcutFolder=shortcut.stdout.trim();
  const target = spawn(fixture,[join(temporary,"target.json"),"AIClaw Target Fixture"],{stdio:"ignore"}); children.push(target);
  const sentinel = spawn(fixture,[join(temporary,"sentinel.json"),"AIClaw Sentinel Fixture"],{stdio:"ignore"}); children.push(sentinel);
  await stageComputerRuntime({repo,destination:join(repo,"apps/desktop"),platform:"win32",arch:process.arch});
  runtime = await ComputerPortableRuntime.create();
  const call = async (action,args={}) => {
    const response=await runtime.call(action,args);
    if(response.isError) throw Error(response.content.map(item=>item.text??"").join("\n"));
    return response;
  };
  const permissions=value(await call("permission_status"));
  assert.ok(permissions.runtime_directory.startsWith(join(homedir(),".aiclaw/computer-use/win32")));
  const app=String(target.pid), sentinelApp=String(sentinel.pid);
  let inventory;
  for(let attempt=0;attempt<50;attempt++) {
    inventory=value(await call("find_apps",{text:"AIClaw Target Fixture"}));
    if(inventory.apps.some(item=>item.id===app)) break;
    await pause(100);
  }
  assert.ok(inventory.apps.some(item=>item.id===app));
  await call("modify_app",{app:sentinelApp,modification:"activate"});
  const front=value(await call("frontmost_window")).app; assert.equal(front,sentinelApp);
  const query=async text=>{
    let result;
    for(let attempt=0;attempt<30;attempt++) {
      result=value(await call("query",{app,text,exact:true}));
      if(result.matches.length) break;
      await pause(100);
    }
    if(result.matches.length!==1) console.error("Fixture AX diagnostic:",JSON.stringify(await call("get_app_state",{app,include_ax:true,include_screenshot:false})));
    assert.equal(result.truncated,false); assert.equal(result.matches.length,1,JSON.stringify(result));
    return result.matches[0];
  };
  const action=(operation,input,background=false)=>call(({click:"click",setValue:"set_value",selectText:"select_text",performSecondaryAction:"perform_secondary_action",pressKey:"press_key",type:"type_text",scroll:"scroll",drag:"drag"})[operation],{app,...input,...(background?{interaction_mode:"background"}:{})});
  const text="Windows 后台 🌦️";
  await action("setValue",{element_index:String((await query("Fixture input")).index),value:text},true);
  await action("selectText",{element_index:String((await query("Fixture input")).index),text,selection:"text"},true);
  await action("performSecondaryAction",{element_index:String((await query("Fixture toggle")).index),action:"toggle"},true);
  await action("click",{element_index:String((await query("Submit fixture")).index)},true);
  let receipt;
  for(let attempt=0;attempt<40;attempt++) { try {receipt=JSON.parse(await readFile(join(temporary,"target.json"),"utf8"));break;} catch {await pause(50);} }
  assert.equal(receipt.value,text);assert.equal(receipt.checked,true);
  assert.equal(value(await call("frontmost_window")).app,front);
  const observed=await call("get_app_state",{app,include_ax:true});
  const image=observed.content.find(item=>item.type==="image");assert.ok(image?.data);
  assert.ok(Buffer.from(image.data,"base64").subarray(0,8).equals(Buffer.from([137,80,78,71,13,10,26,10])));
  const png=join(temporary,"window.png");await writeFile(png,Buffer.from(image.data,"base64"));
  await run(powershell,["-NoProfile","-NonInteractive","-EncodedCommand",encoded('Add-Type -AssemblyName System.Drawing; $image=[Drawing.Bitmap]::FromFile($env:AICLAW_FIXTURE_PNG); try {$red=0;for($y=0;$y -lt $image.Height;$y+=4){for($x=0;$x -lt $image.Width;$x+=4){$c=$image.GetPixel($x,$y);if($c.R -eq 204 -and $c.G -eq 51 -and $c.B -eq 68 -and $c.A -eq 255){$red++}}};if($red -lt 100){throw "Capture did not contain the occluded red target window"}} finally {$image.Dispose()}')],{env:{...process.env,AICLAW_FIXTURE_PNG:png},timeout:30_000});
  const host=(action,args={})=>controller.perform({sessionId:"windows-host-acceptance",turnId:"t1",action,arguments:args});
  const baselineResult=await host("watch_start",{app,interval_ms:500});
  const baseline=JSON.parse(baselineResult.text); assert.ok(baselineResult.imageBase64);
  const quiet=JSON.parse((await host("watch_next",{watch_id:baseline.watch_id,after_sequence:baseline.sequence,wait_ms:900})).text);
  assert.equal(quiet.changed,false);
  const setFromQuery=async value=>{
    const queried=await host("query",{app,text:"Fixture input",exact:true});
    const matches=JSON.parse(queried.text.split("\n")[0]).matches; assert.equal(matches.length,1);
    const observation_id=queried.text.match(/observation_id: ([\w-]+)/)[1];
    await host("set_value",{app,element_index:String(matches[0].index),observation_id,value,interaction_mode:"background",verify_after:false});
  };
  await setFromQuery("Windows 观察更新 🌦️");
  const update=JSON.parse((await host("watch_next",{watch_id:baseline.watch_id,after_sequence:baseline.sequence,wait_ms:5000})).text);
  assert.equal(update.changed,true); assert.equal(update.reset,false);
  const lines=[...baseline.ax.lines]; lines.splice(update.ax.start,update.ax.delete_count,...update.ax.lines);
  assert.ok(lines.some(line=>line.includes("Windows 观察更新 🌦️")));
  await setFromQuery(text);
  await controller.finishTurn("windows-host-acceptance","t1");
  assert.equal(value(await call("frontmost_window")).app,front);
  await call("get_app_state",{app,include_ax:false});
  await action("pressKey",{key:"End"});
  await call("get_app_state",{app,include_ax:false});
  const addition=" +输入 🌤️";
  await action("type",{text:addition});
  const gesture=(await query("Fixture gesture pad")).frame;
  const screen=value(await call("get_app_state",{app,include_ax:false}));
  const x=gesture.x-screen.window_frame.x+20, y=gesture.y-screen.window_frame.y+20;
  await action("drag",{from_x:x,from_y:y,to_x:x+80,to_y:y+40});
  await call("get_app_state",{app,include_ax:false});
  await action("scroll",{x:x+80,y:y+40,direction:"down",pages:1});
  await action("click",{element_index:String((await query("Submit fixture")).index)});
  for(let attempt=0;attempt<40;attempt++) {
    receipt=JSON.parse(await readFile(join(temporary,"target.json"),"utf8"));
    if(receipt.value===text+addition&&receipt.drags>0&&receipt.scrolls>0) break;
    await pause(50);
  }
  assert.equal(receipt.value,text+addition); assert.ok(receipt.drags>0); assert.ok(receipt.scrolls>0);
  const stopped=value(await call("stop_app",{app})); assert.equal(stopped.verified,true);
  const installed=value(await call("find_apps",{text:"AIClaw Launch Fixture"}));
  const launcher=installed.apps.find(item=>item.launch_id); assert.ok(launcher);
  await assert.rejects(call("start_app",{app:fixture}),/exact launch_id/);
  const linkPath=join(shortcutFolder,"AIClaw Launch Fixture.lnk"), linkBytes=await readFile(linkPath);
  await writeFile(linkPath,Buffer.concat([linkBytes,Buffer.from([0])]));
  await assert.rejects(call("start_app",{app:launcher.launch_id}),/shortcut changed/);
  await writeFile(linkPath,linkBytes);
  const started=value(await call("start_app",{app:launcher.launch_id}));
  assert.equal(started.verified,true); launchedPID=Number(started.app);
  await call("get_app_state",{app:started.app});
  assert.equal(value(await call("stop_app",{app:started.app})).verified,true); launchedPID=undefined;
  console.log(JSON.stringify({passed:true,backend:"windows-uia",installed_runtime:true,host_controller:true,static_window_no_update:true,ax_delta_reconstructed:true,source_identity_handshake:true,discovery:true,background_value:text,text_selection:true,secondary_action:true,click_receipt:true,foreground_unchanged:true,window_only_png:true,occluded_target_color_verified:true,unicode_insertion:true,key_read_back:true,drag_events:receipt.drags,scroll_events:receipt.scrolls,normal_close_verified:true,discovered_shortcut_launch:true,changed_shortcut_refused:true,arbitrary_launch_path_refused:true,launched_pid_verified:true,launched_window_observed:true}));
} finally {
  await runtime?.close();
  await controller.close();
  for(const child of children) { if(child.exitCode===null&&child.signalCode===null) child.kill(); }
  if(launchedPID) {try {process.kill(launchedPID);} catch {}}
  if(shortcutFolder) await rm(shortcutFolder,{recursive:true,force:true,maxRetries:10,retryDelay:200});
  await rm(temporary,{recursive:true,force:true,maxRetries:10,retryDelay:200});
}
