import { strict as assert } from "node:assert";
import { mkdirSync, mkdtempSync, readFileSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { test } from "node:test";
import { CommandRunner, shellInvocation, type CommandEvent } from "../apps/desktop/src/main/command-runner.ts";

function fixture() {
 const events: CommandEvent[] = [];
 let finish!: (event: Extract<CommandEvent,{type:"exit"}>) => void;
 const done = new Promise<Extract<CommandEvent,{type:"exit"}>>(resolve => {finish=resolve;});
 const runner = new CommandRunner(event => {events.push(event);if(event.type==="exit")finish(event);});
 return {runner,events,done};
}

test("user command runs in the requested directory and streams output", async () => {
 const dir=mkdtempSync(join(tmpdir(),"aiclaw-command-"));const {runner,events,done}=fixture();
 try {
  runner.start({id:"run-1",sessionId:"session-a",command:`"${process.execPath}" -e "require('fs').writeFileSync('marker.txt','result');process.stdout.write('hello:'+process.cwd());process.stderr.write('stderr')"`,cwd:dir});
  const exit=await done;
  assert.equal(exit.code,0);assert.equal(exit.stopped,false);
  assert.equal(readFileSync(join(dir,"marker.txt"),"utf8"),"result");
  assert.ok(events.some(event=>event.type==="output" && event.text.includes("hello")));
  assert.ok(events.filter(event=>event.type==="output").map(event=>event.text).join("").includes(dir));
  assert.ok(events.some(event=>event.type==="output" && event.text.includes("stderr")));
 } finally {runner.stopAll();rmSync(dir,{recursive:true,force:true});}
});

test("nonzero exit and missing working directory are reported", async () => {
 const a=fixture();a.runner.start({id:"exit",sessionId:"s",command:process.platform === "win32" ? "exit /b 7" : "exit 7",cwd:tmpdir()});assert.equal((await a.done).code,7);
 const b=fixture();b.runner.start({id:"missing",sessionId:"s",command:"pwd",cwd:join(tmpdir(),"aiclaw-directory-that-does-not-exist")});assert.ok((await b.done).error);
});

test("stop is scoped to its session and terminates the running process", async () => {
 const {runner,done}=fixture();runner.start({id:"slow",sessionId:"owner",command:`"${process.execPath}" -e "setTimeout(()=>{},30000)"`,cwd:tmpdir()});
 assert.equal(runner.stop("slow","other"),false);
 assert.throws(()=>runner.start({id:"slow",sessionId:"owner",command:"echo duplicate",cwd:tmpdir()}));
 assert.equal(runner.stop("slow","owner"),true);
 const exit=await Promise.race([done,new Promise<never>((_,reject)=>{const timer=setTimeout(()=>reject(new Error("command did not stop")),15000);timer.unref();})]);
 assert.equal(exit.stopped,true);
 assert.equal(runner.stop("slow","owner"),false);
});

test("invalid command requests do not start a shell", () => {
 const {runner,events}=fixture();
 for (const command of ["", "  ", "echo\0bad"]) assert.throws(()=>runner.start({id:"id",sessionId:"s",command,cwd:tmpdir()}));
 assert.throws(()=>runner.start({id:"../bad",sessionId:"s",command:"echo ok",cwd:tmpdir()}));
 assert.equal(events.length,0);
});


test("window reload stops commands while keeping runner available; shutdown closes it", async () => {
 const {runner,done}=fixture();
 runner.start({id:"reload",sessionId:"s",command:`"${process.execPath}" -e "setTimeout(()=>{},30000)"`,cwd:tmpdir()});
 runner.stopAll(false);
 assert.equal((await done).stopped,true);
 runner.start({id:"again",sessionId:"s",command:`"${process.execPath}" -e "setTimeout(()=>{},30000)"`,cwd:tmpdir()});
 runner.stopAll();
 assert.throws(()=>runner.start({id:"closed",sessionId:"s",command:"echo bad",cwd:tmpdir()}));
});


test("system shell receives the original command on Unix and correctly quoted cmd.exe input on Windows", () => {
 const command = `"C:\\Program Files\\node.exe" -e "console.log('hello')"`;
 assert.deepEqual(shellInvocation(command,"win32",{ComSpec:"C:\\Windows\\cmd.exe"}), {shell:"C:\\Windows\\cmd.exe",args:["/d","/s","/c",`"${command}"`],verbatim:true});
 assert.deepEqual(shellInvocation("printf hi","darwin",{SHELL:"/bin/zsh"}), {shell:"/bin/zsh",args:["-c","printf hi"],verbatim:false});
});


test("user-clicked command uses the system environment outside the workspace", async () => {
 const dir=mkdtempSync(join(tmpdir(),"aiclaw-unsandboxed-")), cwd=join(dir,"workspace");mkdirSync(cwd);
 const {runner,done}=fixture();
 try {
  runner.start({id:"system",sessionId:"s",command:`"${process.execPath}" -e "require('fs').writeFileSync('../outside.txt','allowed')"`,cwd});
  assert.equal((await done).code,0);
  assert.equal(readFileSync(join(dir,"outside.txt"),"utf8"),"allowed");
 } finally {runner.stopAll();rmSync(dir,{recursive:true,force:true});}
});

test("large output is drained but limited in the UI", async () => {
 const {runner,events,done}=fixture();
 runner.start({id:"large",sessionId:"s",command:`"${process.execPath}" -e "process.stdout.write('x'.repeat(1200000))"`,cwd:tmpdir()});
 assert.equal((await done).code,0);
 const output=events.filter((event):event is Extract<CommandEvent,{type:"output"}>=>event.type==="output").map(event=>event.text).join("");
 assert.ok(output.startsWith("x".repeat(1000)));
 assert.ok(output.includes("Output truncated"));
 assert.ok(output.length < 1_000_100);
});
