import test from "node:test";
import assert from "node:assert/strict";
import { ComputerController } from "../apps/desktop/src/main/computer.ts";
import {
  backgroundCuaArguments,
  cuaToolSpecs,
} from "../apps/desktop/src/main/cua-policy.ts";
import type {
  ComputerRuntime,
  ComputerToolResult,
} from "../apps/desktop/src/main/computer-runtime.ts";
const target = { pid: 1234, window_id: 987 };
const request = (
  action: string,
  args = {},
  turnId = "turn",
  sessionId = "chat",
) => ({ sessionId, turnId, action, arguments: args });
const text = (value = "ok"): ComputerToolResult => ({
  content: [{ type: "text", text: value }],
});
function runtime(
  call: ComputerRuntime["call"],
  close: ComputerRuntime["close"] = async () => {},
): ComputerRuntime {
  return {
    tools: cuaToolSpecs.map((spec) => ({
      name: spec.action,
      inputSchema: spec.schema,
    })),
    call,
    close,
  };
}
function deferred<T>() {
  let resolve!: (value: T) => void, reject!: (error: Error) => void;
  const promise = new Promise<T>((yes, no) => {
    resolve = yes;
    reject = no;
  });
  return { promise, resolve, reject };
}
test("all input routes force background and reject model foreground/session/desktop overrides", () => {
  for (const action of [
    "click",
    "type_text",
    "press_key",
    "hotkey",
    "scroll",
    "drag",
  ]) {
    const args = {
      ...target,
      ...(action === "type_text"
        ? { text: "中文" }
        : action === "press_key"
          ? { key: "enter" }
          : action === "hotkey"
            ? { keys: ["CMD", "A"] }
            : action === "scroll"
              ? { direction: "down", amount: 1 }
              : action === "drag"
                ? { from_x: 1, from_y: 2, to_x: 3, to_y: 4 }
                : { x: 1, y: 2 }),
    };
    assert.equal(
      backgroundCuaArguments(action, args).delivery_mode,
      "background",
    );
    for (const override of [
      { delivery_mode: "foreground" },
      { session: "other" },
      { scope: "desktop" },
      { target: { kind: "desktop", display_id: "primary" } },
    ])
      assert.throws(
        () => backgroundCuaArguments(action, { ...args, ...override }),
        /Unsupported/,
      );
  }
  for (const action of [
    "run_script",
    "bring_to_front",
    "get_desktop_state",
    "clipboard_write",
    "set_agent_cursor_enabled",
    "move_cursor",
  ])
    assert.throws(() => backgroundCuaArguments(action, {}), /Unsupported/);
  assert.throws(
    () => backgroundCuaArguments("click", { pid: process.pid, window_id: 1 }),
    /non-host/,
  );
});
test("batch normalizes semantic actions, one exact window and final observation without escalation", () => {
  const args = backgroundCuaArguments("run_steps", {
    ...target,
    steps: [
      {
        tool: "set_value",
        args: { role: "textfield", name: "Input", value: "中文" },
      },
      {
        tool: "click",
        args: { role: "button", name: "Save" },
        expect: [{ text: "Saved" }],
      },
    ],
    observe: { include_screenshot: true },
  });
  assert.equal(args.foreground_fallback, true);
  assert.equal(args.delivery_mode, undefined);
  const steps = args.steps as any[];
  assert.equal(steps[1].args.delivery_mode, undefined);
  assert.equal(steps[0].args.value, "中文");
  assert.equal(steps[0].args.delivery_mode, undefined);
  for (const step of [
    { tool: "click", args: { delivery_mode: "foreground" } },
    { tool: "click", args: { target: { kind: "desktop" } } },
    { tool: "click", args: { pid: 1, window_id: 2 } },
    { click: {}, tool: "click", args: {} },
    { tool: "run_script", args: {} },
    { expect: { app: "AIClaw", text: "foo" } },
    { tool: "click", args: {}, session: "other" },
  ])
    assert.throws(() =>
      backgroundCuaArguments("run_steps", { ...target, steps: [step] }),
    );
  assert.throws(() =>
    backgroundCuaArguments("run_steps", {
      ...target,
      steps: [{ expect: { text: "Saved" } }],
      observe: { session: "other" },
    }),
  );
});
test("pause cancels active lease immediately and drops queued work; resume creates a fresh lease", async () => {
  const entered = deferred<void>(),
    pending = deferred<ComputerToolResult>();
  let calls = 0,
    closed = 0;
  const controller = new ComputerController(async () =>
    runtime(
      async () => {
        calls++;
        if (calls === 1) {
          entered.resolve();
          return pending.promise;
        }
        return text();
      },
      async () => {
        closed++;
        pending.reject(Error("closed"));
      },
    ),
  );
  const active = controller.perform(request("list_apps"));
  await entered.promise;
  const queued = controller.perform(request("list_apps"));
  const outcomes = Promise.allSettled([active, queued]);
  await controller.setControl("pause");
  assert.equal(closed, 1);
  assert.ok((await outcomes).every((result) => result.status === "rejected"));
  assert.equal(calls, 1);
  await controller.setControl("resume");
  await controller.perform(request("list_apps"));
  assert.equal(calls, 2);
  await controller.close();
});
test("turn completion during connection startup closes it without dispatch", async () => {
  const startup = deferred<ComputerRuntime>();
  let calls = 0,
    closed = 0;
  const controller = new ComputerController(() => startup.promise);
  const active = controller.perform(request("list_apps"));
  await new Promise((resolve) => setImmediate(resolve));
  await controller.finishTurn("chat", "turn");
  startup.resolve(
    runtime(
      async () => {
        calls++;
        return text();
      },
      async () => {
        closed++;
      },
    ),
  );
  await assert.rejects(active, /ended/);
  assert.equal(calls, 0);
  assert.equal(closed, 1);
  await controller.close();
});
test("failed batch is surfaced without replay; fresh lease required for a later observation", async () => {
  let calls = 0,
    closed = 0;
  const controller = new ComputerController(async () =>
    runtime(
      async () => {
        calls++;
        return {
          ...text("run_steps: step 2 failed; outcome unknown"),
          isError: true,
        };
      },
      async () => {
        closed++;
      },
    ),
  );
  await assert.rejects(
    controller.perform(
      request("run_steps", {
        ...target,
        steps: [{ tool: "click", args: { x: 1, y: 2 } }],
      }),
    ),
    /Do not replay/,
  );
  assert.equal(calls, 1);
  assert.equal(closed, 1);
  await controller.close();
});
test("chats have separate turn leases, close only their own lease, and publish background target", async () => {
  let created = 0,
    closed = 0;
  const states: any[] = [];
  const controller = new ComputerController(async () => {
    created++;
    return runtime(
      async () => text(),
      async () => {
        closed++;
      },
    );
  });
  controller.subscribe((state) => states.push(state));
  await controller.perform(request("click", { ...target, x: 1, y: 2 }));
  await controller.perform(request("list_apps", {}, "t2", "other"));
  assert.equal(created, 2);
  assert.ok(states.some((state) => state.active?.pid === 1234));
  await controller.releaseSession("chat");
  assert.equal(closed, 1);
  await controller.close();
  assert.equal(closed, 2);
});
test("permission diagnostics are read-only; only explicit setup may prompt", async () => {
  const prompts: unknown[] = [];
  const controller = new ComputerController(
    async () =>
      runtime(async (_name, args) => {
        prompts.push(args.prompt);
        return {
          ...text("permission report"),
          structuredContent: { accessibility: true, screen_recording: true },
        };
      }),
    async () => {
      prompts.push("host-setup");
    },
  );
  assert.equal((await controller.diagnostics()).screenCapture, true);
  assert.deepEqual(prompts, [false]);
  await controller.showPermissionSetup();
  assert.deepEqual(prompts, [false, "host-setup", false]);
  await controller.close();
});

test("batch failure uses its receipt, not JSON text in the observed document", async () => {
  let ok=true;
  const controller=new ComputerController(async()=>runtime(async()=>({...text('A document can contain {"success":false}'),structuredContent:{ok}})));
  const batch=request("run_steps",{...target,steps:[{expect:{text:"Saved"}}]});
  assert.equal((await controller.perform(batch)).verificationFailed,undefined);
  ok=false;assert.equal((await controller.perform(request("run_steps",{...target,steps:[{expect:{text:"Other"}}]}))).verificationFailed,true);
  await controller.close();
});

test("a duplicate resume preserves the active execution marker", async () => {
  const entered=deferred<void>(),pending=deferred<ComputerToolResult>();
  const controller=new ComputerController(async()=>runtime(async()=>{entered.resolve();return pending.promise;}));
  const work=controller.perform(request("list_apps"));await entered.promise;
  await controller.setControl("resume");assert.equal(controller.getStatus().active?.action,"list_apps");
  pending.resolve(text());await work;await controller.close();
});

test("an app that defeats background launch stops the automation lease", async () => {
  let closed=0;
  const controller=new ComputerController(async()=>runtime(async()=>({...text("running"),structuredContent:{self_activation_suppressed:false}}),async()=>{closed++;}));
  await assert.rejects(controller.perform(request("launch_app",{bundle_id:"com.apple.calculator"})),/did not stay in the background/);
  assert.equal(closed,1);await controller.close();
});

test("window wait ignores untitled helpers and yields the new titled window", async () => {
  let reads=0;
  const controller=new ComputerController(async()=>runtime(async()=>{
    reads++;return {...text("windows"),structuredContent:{windows:[{pid:1234,window_id:10,layer:0,title:"Old"},{pid:1234,window_id:11,layer:0,title:""},...(reads>1?[{pid:1234,window_id:12,layer:0,title:"New"}]:[])]}};
  }));
  const result=await controller.perform(request("wait_new_window",{pid:1234,exclude_window_ids:[10],timeout_ms:1000}));
  assert.deepEqual(JSON.parse(result.text).data.windows.map((w:any)=>w.window_id),[12]);assert.equal(reads,2);await controller.close();
});

test("repeated input is refused until a comparable observation confirms a change",async()=>{
 let state="before",clicks=0;
 const controller=new ComputerController(async()=>runtime(async(action)=>{
   if(action==="get_window_state")return {...text("window"),structuredContent:{snapshot_id:Math.random().toString(),tree_markdown:state}};
   clicks++;return text("dispatched");
 }));
 const observe=()=>controller.perform(request("get_window_state",{...target,include_screenshot:false}));
 const click=()=>controller.perform(request("hotkey",{...target,keys:["cmd","s"]}));
 await observe();await click();await observe();await assert.rejects(click(),/no changed state/);assert.equal(clicks,1);
 state="saved";await observe();await click();assert.equal(clicks,2);await controller.close();
});

test("stale token rejection keeps the current session and fresh snapshot",async()=>{
 let created=0,closed=0;
 const controller=new ComputerController(async()=>{created++;return runtime(async(action)=>action==="click"?{isError:true,...text("element_token is stale; no input sent")}:text("read"),async()=>{closed++;});});
 await assert.rejects(controller.perform(request("click",{...target,element_token:"old"})),/stale/);
 await controller.perform(request("list_apps"));assert.equal(created,1);assert.equal(closed,0);await controller.close();
});

test("stale hotkey token may be replaced without losing the live session",async()=>{
 let attempts=0,created=0;
 const controller=new ComputerController(async()=>{created++;return runtime(async()=>++attempts===1?{isError:true,...text("element_token is stale; no input sent")}:text("sent"));});
 const hotkey=(element_token:string)=>controller.perform(request("hotkey",{...target,keys:["cmd","s"],element_token}));
 await assert.rejects(hotkey("stale"),/stale/);await hotkey("fresh");assert.equal(attempts,2);assert.equal(created,1);await controller.close();
});

test("an explicitly refused background menu action uses foreground and restores the original window",async()=>{
 const calls:{action:string,args:any}[]=[];let active=444, menus=0;
 const controller=new ComputerController(async()=>runtime(async(action,args)=>{
   calls.push({action,args});
   if(action==="list_apps")return {...text("apps"),structuredContent:{apps:[{pid:active,active:true}]}};
   if(action==="list_windows")return {...text("windows"),structuredContent:{windows:[{pid:Number(args.pid),window_id:args.pid===1234?987:555,layer:0,is_on_screen:true,z_index:1}]}};
   if(action==="bring_to_front"){active=Number(args.pid);return text("activated");}
   if(action==="invoke_menu" && ++menus===1)return {...text('menu is disabled while the app is in the background'),isError:true};
   return text("saved");
 }));
 const states:any[]=[];controller.subscribe(state=>states.push(state));
 const output=await controller.perform(request("invoke_menu",{...target,path:["File","Save"]}));
 assert.ok(states.some(state=>state.active?.foreground===true));
 assert.match(output.text,/Temporary foreground execution was used[\s\S]*saved/);assert.deepEqual(calls.filter(x=>x.action==="bring_to_front").map(x=>x.args.pid),[1234,444]);
 assert.deepEqual(calls.filter(x=>x.action==="invoke_menu").map(x=>x.args.delivery_mode),["background","foreground"]);
 await controller.close();
});

test("foreground recovery never overrides a later user focus change",async()=>{
 let active=444,menus=0;const activations:number[]=[];
 const controller=new ComputerController(async()=>runtime(async(action,args)=>{
   if(action==="list_apps")return {...text("apps"),structuredContent:{apps:[{pid:active,active:true}]}};
   if(action==="list_windows")return {...text("windows"),structuredContent:{windows:[{window_id:args.pid===1234?987:555,layer:0,is_on_screen:true,z_index:1}]}};
   if(action==="bring_to_front"){active=Number(args.pid);activations.push(active);return text("activated");}
   if(action==="invoke_menu" && ++menus===1)return {...text("background_unavailable"),isError:true};
   if(action==="invoke_menu")active=999;
   return text("done");
 }));
 await controller.perform(request("invoke_menu",{...target,path:["File","Save"]}));assert.deepEqual(activations,[1234]);await controller.close();
});

test("uncertain input failures never trigger a foreground retry",async()=>{
 let calls=0;
 const controller=new ComputerController(async()=>runtime(async()=>{calls++;return {...text("input timed out; outcome unknown"),isError:true};}));
 await assert.rejects(controller.perform(request("click",{...target,x:1,y:2})),/outcome unknown/);assert.equal(calls,1);await controller.close();
});

test("window discovery deadline cancels a hung driver request",async()=>{
 let closed=0;const pending=deferred<ComputerToolResult>();
 const controller=new ComputerController(async()=>runtime(async()=>pending.promise,async()=>{closed++;pending.reject(Error("closed"));}));
 await assert.rejects(controller.perform(request("wait_new_window",{pid:1234,exclude_window_ids:[],timeout_ms:100})),/discovery timed out/);
 assert.equal(closed,1);await controller.close();
});

test("structured window state exposes the driver's current token without guessing",async()=>{
 const controller=new ComputerController(async()=>runtime(async()=>({...text("window read"),structuredContent:{snapshot_id:"s0000000a",elements:[{element_token:"s0000000a:7",role:"AXTextArea"}]}})));
 const output=await controller.perform(request("get_window_state",{...target,include_screenshot:false,tree_format:"elements"}));
 assert.equal(JSON.parse(output.text).data.elements[0].element_token,"s0000000a:7");await controller.close();
});

test("reordered arguments cannot bypass the duplicate input guard",async()=>{
 let calls=0;
 const controller=new ComputerController(async()=>runtime(async()=>{calls++;return text("sent");}));
 await controller.perform(request("hotkey",{pid:1234,window_id:987,keys:["cmd","s"]}));
 await assert.rejects(controller.perform(request("hotkey",{keys:["cmd","s"],window_id:987,pid:1234})),/already dispatched/);
 assert.equal(calls,1);await controller.close();
});

test("a fresh element token alone is not evidence of a changed window",async()=>{
 let reads=0,clicks=0;
 const controller=new ComputerController(async()=>runtime(async(action)=>action==="get_window_state"
   ? {...text("window"),structuredContent:{snapshot_id:`s${++reads}`,elements:[{element_token:`s${reads}:1`,role:"AXTextArea",value:"unchanged"}]}}
   : (clicks++,text("sent"))));
 const observe=()=>controller.perform(request("get_window_state",{...target,include_screenshot:false,tree_format:"elements"}));
 const save=()=>controller.perform(request("hotkey",{...target,keys:["cmd","s"]}));
 await observe();await save();await observe();await assert.rejects(save(),/no changed state/);assert.equal(clicks,1);await controller.close();
});

test("repeated navigation keys remain usable without a screenshot per key",async()=>{
 let calls=0;const controller=new ComputerController(async()=>runtime(async()=>{calls++;return text("sent");}));
 const left=()=>controller.perform(request("press_key",{...target,key:"left"}));
 await left();await left();assert.equal(calls,2);await controller.close();
});

test("a refused background click retries once in foreground without an extra approval",async()=>{
 const modes:string[]=[];
 const controller=new ComputerController(async()=>runtime(async(_action,args)=>{
   modes.push(String(args.delivery_mode));return modes.length===1?{...text("background_unavailable"),isError:true}:text("clicked");
 }));
 const result=await controller.perform(request("click",{...target,x:3,y:4}));
 assert.match(result.text,/Temporary foreground execution was used[\s\S]*clicked/);assert.deepEqual(modes,["background","foreground"]);await controller.close();
});

test("a partially executed batch is never replayed in foreground",async()=>{
 let calls=0;
 const controller=new ComputerController(async()=>runtime(async()=>{calls++;return {...text("background_unavailable after earlier steps"),isError:true};}));
 await assert.rejects(controller.perform(request("run_steps",{...target,steps:[{tool:"click",args:{x:3,y:4}}]})),/background_unavailable/);
 assert.equal(calls,1);await controller.close();
});

test("foreground action continues with a warning if the previous app cannot be identified",async()=>{
 let tries=0;
 const controller=new ComputerController(async()=>runtime(async(action)=>{
   if(action==="list_apps")return {...text("apps"),structuredContent:{apps:[]}};
   if(action==="invoke_menu" && ++tries===1)return {...text("background_unavailable"),isError:true};
   return text("done");
 }));
 const result=await controller.perform(request("invoke_menu",{...target,path:["File","Save"]}));
 assert.match(result.text,/previous foreground window could not be identified/);assert.match(result.text,/done/);await controller.close();
});

test("foreground menu adapts a uniquely changed ellipsis only after a verified no-action refusal",async()=>{
 const paths:string[]=[];let active=444;
 const controller=new ComputerController(async()=>runtime(async(action,args)=>{
   if(action==="list_apps")return {...text("apps"),structuredContent:{apps:[{pid:active,active:true}]}};
   if(action==="list_windows")return {...text("windows"),structuredContent:{windows:[{window_id:args.pid===1234?987:555,layer:0,is_on_screen:true,z_index:1}]}};
   if(action==="bring_to_front"){active=Number(args.pid);return text("activated");}
   if(action==="invoke_menu"){
     paths.push((args.path as string[]).at(-1)!);
     if(paths.length===1)return {...text("background_unavailable"),isError:true};
     if(paths.length===2)return {...text('invoke_menu: path segment 1 "保存" was not found under "文件". Items there: "保存…", "保存为…"'),isError:true};
   }
   return text("done");
 }));
 await controller.perform(request("invoke_menu",{...target,path:["文件","保存"]}));
 assert.deepEqual(paths,["保存","保存","保存…"]);await controller.close();
});

test("driver structured foreground refusal is recognized without parsing its prose",async()=>{
 const modes:string[]=[];
 const controller=new ComputerController(async()=>runtime(async(_action,args)=>{
   modes.push(String(args.delivery_mode));
   return modes.length===1?{...text("unsupported"),isError:true,structuredContent:{refusal:{escalation:{recommended:"foreground"}}}}:text("done");
 }));
 await controller.perform(request("click",{...target,x:1,y:2}));assert.deepEqual(modes,["background","foreground"]);await controller.close();
});

test("restoring focus does not override a user switching windows within the target app",async()=>{
 let active=444,menus=0;const activations:number[]=[];
 const controller=new ComputerController(async()=>runtime(async(action,args)=>{
   if(action==="list_apps")return {...text("apps"),structuredContent:{apps:[{pid:active,active:true}]}};
   if(action==="list_windows")return {...text("windows"),structuredContent:{windows:[{window_id:args.pid===1234?999:555,layer:0,is_on_screen:true,z_index:1}]}};
   if(action==="bring_to_front"){active=Number(args.pid);activations.push(active);return text("activated");}
   if(action==="invoke_menu" && ++menus===1)return {...text("background_unavailable"),isError:true};
   return text("done");
 }));
 const result=await controller.perform(request("invoke_menu",{...target,path:["File","Save"]}));
 assert.deepEqual(activations,[1234]);assert.match(result.text,/active window changed/);await controller.close();
});

test("strict background forbids foreground retry for direct and batch actions", async () => {
 const calls: Array<{action:string; args:Record<string,unknown>}> = [];
 const controller = new ComputerController(async () => runtime(async (action,args) => {
   calls.push({action,args});
   return {...text("background_unavailable"), isError:true};
 }));
 await assert.rejects(controller.perform({...request("click",{...target,x:1,y:2}),backgroundOnly:true}), /requires foreground/);
 await assert.rejects(controller.perform({...request("run_steps",{...target,steps:[{tool:"click",args:{x:1,y:2}}]},"batch"),backgroundOnly:true}), /requires foreground/);
 assert.equal(calls.length,2);
 assert.equal(calls[0].args.delivery_mode,"background");
 assert.equal(calls[1].args.foreground_fallback,false);
 await controller.close();
});

test("screen lock blocks further computer calls until the turn ends", async () => {
 let calls=0;
 const controller = new ComputerController(async () => runtime(async () => {
   calls++; return {...text("Background input refused (screen_locked)"),isError:true};
 }));
 await assert.rejects(controller.perform(request("click",{...target,x:1,y:2})),/screen_locked/);
 await assert.rejects(controller.perform(request("list_windows",{})),/screen_locked/);
 assert.equal(calls,1);
 await controller.finishTurn("chat","turn");
 await assert.rejects(controller.perform(request("click",{...target,x:1,y:2},"next")),/screen_locked/);
 assert.equal(calls,2); await controller.close();
});

test("repeated empty full accessibility reads stop the turn; filtered reads do not", async () => {
 let calls=0;
 const controller = new ComputerController(async () => runtime(async () => {
   calls++; return {...text("window"),structuredContent:{elements:[]}};
 }));
 const read=(args:Record<string,unknown>={})=>controller.perform(request("get_window_state",{...target,tree_format:"elements",...args}));
 await read({query:"missing"}); await read({query:"missing"}); await read({query:"missing"});
 await read(); await read();
 await assert.rejects(read(),/three consecutive/);
 await assert.rejects(read(),/three consecutive/);
 assert.equal(calls,6);await controller.close();
});

test("locked-screen refusal never escalates even when foreground is recommended", async () => {
 let calls=0;
 const controller = new ComputerController(async () => runtime(async () => {
   calls++; return {...text("screen_locked"),isError:true,structuredContent:{refusal:{escalation:{recommended:"foreground"}}}};
 }));
 await assert.rejects(controller.perform(request("click",{...target,x:1,y:2})),/screen_locked/);
 assert.equal(calls,1); await controller.close();
});

test("a nonempty full read resets the empty-read counter", async () => {
 let calls=0;
 const controller = new ComputerController(async () => runtime(async () => {
   calls++; return {...text("window"),structuredContent:{total_element_count:calls===3?1:0}};
 }));
 const read=()=>controller.perform(request("get_window_state",{...target,tree_format:"elements"}));
 await read();await read();await read();await read();await read();
 await assert.rejects(read(),/three consecutive/);await controller.close();
});


test("strict background refusal blocks subsequent desktop operations for the turn", async () => {
 let calls=0;
 const controller = new ComputerController(async () => runtime(async () => { calls++; return {...text("disabled while the app is in the background"),isError:true}; }));
 const opts={backgroundOnly:true};
 await assert.rejects(controller.perform({...request("invoke_menu",{...target,path:["File","Save"]}),...opts}),/requires foreground/);
 await assert.rejects(controller.perform({...request("hotkey",{...target,keys:["cmd","s"]}),...opts}),/requires foreground/);
 assert.equal(calls,1); await controller.close();
});

test("only intermediate navigation keys skip individual watches when batch observes final state", () => {
 const arrow=(extra:Record<string,unknown>={})=>({tool:"press_key",args:{key:"left",...extra}});
 const batch=backgroundCuaArguments("run_steps",{...target,steps:[arrow(),arrow({modifiers:["shift"]}),arrow({modifiers:["cmd"]}),arrow({post_action_wait:"default"}),arrow()],observe:{}},"darwin");
 const steps=batch.steps as any[];
 assert.equal(steps[0].args.post_action_wait,"none");
 assert.equal(steps[1].args.post_action_wait,"none");
 assert.equal(steps[2].args.post_action_wait,undefined);
 assert.equal(steps[3].args.post_action_wait,"default");
 assert.equal(steps[4].args.post_action_wait,undefined);
 const noObserve=backgroundCuaArguments("run_steps",{...target,steps:[arrow(),arrow()]},"darwin");
 assert.equal((noObserve.steps as any[])[0].args.post_action_wait,undefined);
});

test("window captures use platform-supported PNG parameters", () => {
 for (const platform of ["linux", "win32"] as const) {
  const capture = backgroundCuaArguments("get_window_state", {...target, screenshot_format:"png"}, platform);
  assert.equal(capture.screenshot_format,undefined);
  const batch = backgroundCuaArguments("run_steps", {...target, steps:[{tool:"press_key",args:{key:"left"}}], observe:{screenshot_format:"png"}}, platform);
  assert.equal((batch.observe as Record<string,unknown>).screenshot_format,undefined);
 }
 assert.equal(backgroundCuaArguments("get_window_state",target,"darwin").screenshot_format,"png");
});

test("Linux adapts unsupported options without weakening action guarantees", () => {
 assert.throws(()=>backgroundCuaArguments("click",{...target,action:"AXPress"},"linux"),/does not support/);
 assert.throws(()=>backgroundCuaArguments("launch_app",{bundle_id:"com.example.app",creates_new_application_instance:true},"linux"),/cannot guarantee/);
 assert.throws(()=>backgroundCuaArguments("get_window_state",{...target,screenshot_quality:80},"linux"),/do not support/);
 assert.throws(()=>backgroundCuaArguments("press_key",{...target,key:"left",post_action_wait:"default"},"linux"),/does not support/);
 assert.equal(backgroundCuaArguments("press_key",{...target,key:"left",post_action_wait:"none"},"linux").post_action_wait,undefined);
 const b=backgroundCuaArguments("run_steps",{...target,steps:[{tool:"press_key",args:{key:"left"}},{tool:"press_key",args:{key:"left"}}],observe:{}},"linux");
 assert.equal((b.steps as any[])[0].args.post_action_wait,undefined);
});
