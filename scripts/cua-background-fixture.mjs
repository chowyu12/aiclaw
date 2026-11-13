/** Interactive acceptance only. Uses disposable apps, never real documents or chats. */
import assert from "node:assert/strict";
import { spawn, execFileSync } from "node:child_process";
import { mkdtemp, mkdir, writeFile, readFile, rm } from "node:fs/promises";
import { join } from "node:path";
import { fileURLToPath } from "node:url";
import { setTimeout as pause } from "node:timers/promises";
export async function backgroundFixtureChecks(evaluate) {
  if (process.platform !== "darwin")
    throw Error("Background fixture currently requires macOS");
  const directory = await mkdtemp("/tmp/aiclaw-cua-acceptance-");
  const children = [];
  let executable;
  const read = async (name) =>
    JSON.parse(await readFile(join(directory, name), "utf8"));
  const wait = async (name) => {
    for (let attempt = 0; attempt < 100; attempt++) {
      try {
        return await read(name);
      } catch {
        await pause(50);
      }
    }
    throw Error(`Fixture did not start: ${name}`);
  };
  try {
    const binary = join(directory, "Fixture");
    execFileSync(
      "xcrun",
      [
        "swiftc",
        fileURLToPath(
          new URL("../tools/cua-driver/fixtures/Window.swift", import.meta.url),
        ),
        "-o",
        binary,
      ],
      { stdio: "pipe" },
    );
    for (const name of ["Target"]) {
      const bundle = join(directory, `${name}.app/Contents`);
      await mkdir(join(bundle, "MacOS"), { recursive: true });
      const { copyFile } = await import("node:fs/promises");
      await copyFile(binary, join(bundle, "MacOS/Fixture"));
      await writeFile(
        join(bundle, "Info.plist"),
        `<?xml version="1.0"?><plist version="1.0"><dict><key>CFBundleIdentifier</key><string>com.aiclaw.fixture.${name.toLowerCase()}</string><key>CFBundleName</key><string>AIClaw ${name} Fixture</string><key>CFBundleExecutable</key><string>Fixture</string></dict></plist>`,
      );
    }
    executable = join(directory, "Target.app/Contents/MacOS/Fixture");
    const probe = () =>
      JSON.parse(execFileSync(executable, ["--probe"], { encoding: "utf8" }));
    children.push(
      spawn(executable, [join(directory, "target.json")], { stdio: "ignore" }),
    );
    const target = await wait("target.json");
    // Do not activate a foreground fixture: the user keeps their actual app.
    const before = probe();
    const result = await evaluate(`(async()=>{
    const require=process.getBuiltinModule('module').createRequire(process.resourcesPath+'/app/package.json');
    const {CuaRuntime}=require(process.resourcesPath+'/app/dist/main/cua-runtime.js');
    const {ComputerController}=require(process.resourcesPath+'/app/dist/main/computer.js');
    const phaseProbe=()=>JSON.parse(require('node:child_process').execFileSync(${JSON.stringify(executable)},['--probe'],{encoding:'utf8'}));
    const phases=[{name:'before_driver',...phaseProbe()}];
    const runtime=await CuaRuntime.create();phases.push({name:'after_driver',...phaseProbe()});const controller=new ComputerController(async()=>runtime);
    const invoke=(action,args)=>controller.perform({sessionId:'fixture',turnId:'acceptance',action,arguments:args});
    try{
      const target=${JSON.stringify({ pid: target.pid, window_id: target.window_id })};
      const inventory=JSON.parse((await invoke('list_windows',{pid:target.pid})).text).data;
      const waited=JSON.parse((await invoke('wait_new_window',{pid:target.pid,exclude_window_ids:[target.window_id],timeout_ms:300})).text).data;
      if(!inventory.windows.some(w=>w.window_id===target.window_id)) throw Error('Target window missing from inventory: '+JSON.stringify(inventory));
      if(!waited.timed_out || waited.windows.length) throw Error('Window wait failed to exclude the target: '+JSON.stringify(waited));
      const first=await invoke('get_window_state',{...target,include_screenshot:true,max_elements:100});
      if(!first.imageBase64)throw Error('Missing target screenshot');phases.push({name:'after_observe',...phaseProbe()});
      const batch=await invoke('run_steps',{...target,steps:[
        {tool:'set_value',args:{role:'textfield',name:'Fixture Input',value:'后台输入已完成 ✅'}},
        {tool:'click',args:{role:'button',name:'Commit Fixture'},expect:[{role:'textfield',name:'Fixture Input',value:'后台输入已完成 ✅'}]}
      ],observe:{include_screenshot:true}});
      phases.push({name:'after_batch',...phaseProbe()});const cursor=await runtime.call('get_agent_cursor_state',{});
      return {phases,screenshot:true,batch:batch.text,postScreenshot:!!batch.imageBase64,cursor:cursor.structuredContent??cursor.content};
    }finally{await controller.close();}
  })()`);
    const after = probe(),
      finalTarget = await read("target.json");
    assert.equal(finalTarget.value, "后台输入已完成 ✅");
    assert.equal(finalTarget.committed, true);

    assert.equal(
      after.pid,
      before.pid,
      "Foreground changed during the fixture run",
    );
    assert.deepEqual([after.x, after.y], [before.x, before.y]);
    for (const phase of result.phases)
      assert.equal(
        phase.pid,
        before.pid,
        `Foreground changed at ${phase.name}`,
      );
    assert.equal(result.postScreenshot, true);
    return {
      background_batch: true,
      chinese_input_and_click: true,
      user_focus_unchanged: true,
      user_pointer_unchanged: true,
      cursor: result.cursor,
    };
  } finally {
    // Never activate or restore any app: this test does not own the user focus.
    for (const child of children) child.kill();
    await pause(100);
    await rm(directory, { recursive: true, force: true });
  }
}
