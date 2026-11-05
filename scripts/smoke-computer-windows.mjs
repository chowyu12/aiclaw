/** Windows-only real native acceptance. Only temporary WinForms apps receive input. */
import assert from "node:assert/strict";
import { spawn, execFile } from "node:child_process";
import { createHash } from "node:crypto";
import { once } from "node:events";
import { mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { dirname, join, resolve } from "node:path";
import { Duplex } from "node:stream";
import { setTimeout as pause } from "node:timers/promises";
import { fileURLToPath } from "node:url";
import { promisify } from "node:util";
if (process.platform !== "win32") throw Error("This acceptance requires Windows");
const run = promisify(execFile);
const repo = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const directory = join(repo, "tools/computer-use/windows");
const powershell = join(process.env.SystemRoot, "System32/WindowsPowerShell/v1.0/powershell.exe");
const temporary = await mkdtemp(join(tmpdir(), "aiclaw-windows-acceptance-"));
const children = []; let runtime;
const encoded = source => Buffer.from(source, "utf16le").toString("base64");
const environment = { ...process.env, AICLAW_COMPUTER_SERVICE: join(directory,"Service.ps1"), AICLAW_COMPUTER_MANIFEST: join(repo,"tools/claw-agent/internal/agent/computer-tools.json"), AICLAW_COMPUTER_PARENT_PID: String(process.pid) };
const { ComputerPortableRuntime } = await import(new URL("../apps/desktop/dist/main/computer-portable-runtime.js", import.meta.url));
const value = result => JSON.parse(result.content.find(item => item.type === "text").text.split("\n")[0]);
try {
  const fixture = join(temporary,"AIClawFixture.exe");
  await run(powershell,["-NoProfile","-NonInteractive","-EncodedCommand",encoded('Add-Type -TypeDefinition ([IO.File]::ReadAllText($env:AICLAW_FIXTURE_SOURCE)) -ReferencedAssemblies System.dll,System.Drawing.dll,System.Windows.Forms.dll,System.Web.Extensions.dll -OutputAssembly $env:AICLAW_FIXTURE_EXE -OutputType WindowsApplication')],{env:{...process.env,AICLAW_FIXTURE_SOURCE:join(directory,"Fixture.cs"),AICLAW_FIXTURE_EXE:fixture},timeout:60_000});
  const target = spawn(fixture,[join(temporary,"target.json"),"AIClaw Target Fixture"],{stdio:"ignore"}); children.push(target);
  const sentinel = spawn(fixture,[join(temporary,"sentinel.json"),"AIClaw Sentinel Fixture"],{stdio:"ignore"}); children.push(sentinel);
  const service = spawn(powershell,["-NoProfile","-NonInteractive","-STA","-EncodedCommand",encoded('& ([scriptblock]::Create([IO.File]::ReadAllText($env:AICLAW_COMPUTER_SERVICE))) -Manifest $env:AICLAW_COMPUTER_MANIFEST')],{env:environment,stdio:["pipe","pipe","pipe"],windowsHide:true}); children.push(service);
  service.stderr.on("data",chunk=>process.stderr.write(chunk));
  const transport = Duplex.from({readable:service.stdout,writable:service.stdin});
  service.once("error",error=>transport.destroy(error)); service.once("exit",code=>transport.destroy(code ? Error(`Service exited (${code})`) : undefined));
  const sources = await Promise.all([readFile(join(directory,"Service.ps1")),readFile(join(directory,"Native.cs"))]);
  runtime = new ComputerPortableRuntime(transport,createHash("sha256").update(sources[0]).update(sources[1]).digest("hex"));
  await runtime.initialize();
  const call = async (action,args={}) => {
    const response=await runtime.call(action,args);
    if(response.isError) throw Error(response.content.map(item=>item.text??"").join("\n"));
    return response;
  };
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
  console.log(JSON.stringify({passed:true,backend:"windows-uia",source_identity_handshake:true,discovery:true,background_value:text,text_selection:true,secondary_action:true,click_receipt:true,foreground_unchanged:true,window_only_png:true,occluded_target_color_verified:true}));
} finally {
  await runtime?.close();
  if(children[2]?.exitCode===null&&children[2]?.signalCode===null) await Promise.race([once(children[2],"exit"),pause(1000)]);
  for(const child of children) { if(child.exitCode===null&&child.signalCode===null) child.kill(); }
  await rm(temporary,{recursive:true,force:true,maxRetries:10,retryDelay:200});
}
