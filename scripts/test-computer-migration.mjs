/** Real signed service migration in private temporary roots. No user services,
 * permissions, screenshots, input, signals beyond normal fixture cleanup.
 */
import assert from "node:assert/strict";
import { execFileSync, spawn, spawnSync } from "node:child_process";
import { cpSync, existsSync, mkdirSync, mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { createConnection } from "node:net";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { setTimeout as pause } from "node:timers/promises";
import { buildComputerRuntime } from "./build-computer-runtime.mjs";
import { signMacBundle } from "./mac-signing.mjs";
import { installComputerBundle } from "../apps/desktop/src/main/computer-install.ts";

if (process.platform !== "darwin") { console.log("Legacy migration acceptance requires macOS"); process.exit(0); }
const repo=resolve(dirname(fileURLToPath(import.meta.url)),"..");
const root=mkdtempSync("/tmp/aiclaw-migrate-");
const children=[];let sequence=0;
const waitFor=async (predicate,timeout=3000)=>{
  const deadline=Date.now()+timeout;
  do {if(predicate()) return;await pause(30);} while(Date.now()<deadline);
  throw Error("The owned migration fixture did not reach the expected state");
};
const start=(binary,args)=>{const child=spawn(binary,args,{stdio:["ignore","ignore","pipe"]});children.push(child);return child;};
const handshake=socket=>new Promise((resolve,reject)=>{
  const client=createConnection(socket);let data="";
  const timer=setTimeout(()=>{client.destroy();reject(Error("Legacy fixture handshake timed out"));},2000);
  client.on("connect",()=>client.write('{"id":1,"requestType":"ComputerUseIPCHandshakeRequest","request":{}}\n'));
  client.on("data",chunk=>{data+=chunk;if(!data.includes("\n"))return;clearTimeout(timer);client.end();resolve(JSON.parse(data.split("\n")[0]));});
  client.on("error",error=>{clearTimeout(timer);reject(error);});
});
try {
  const production=buildComputerRuntime({output:join(repo,"build/migration-acceptance-production")});
  const source=join(root,"legacy-source.app"),contents=join(source,"Contents");
  mkdirSync(join(contents,"MacOS"),{recursive:true});
  writeFileSync(join(contents,"Info.plist"),'<?xml version="1.0"?><plist version="1.0"><dict><key>CFBundleIdentifier</key><string>com.github.chowyu12.aiclaw.computer-use</string><key>CFBundleExecutable</key><string>AIClawComputerUse</string><key>CFBundleName</key><string>Legacy Migration Fixture</string><key>CFBundlePackageType</key><string>APPL</string><key>LSUIElement</key><true/></dict></plist>');
  execFileSync("xcrun",["swiftc",join(repo,"scripts/fixtures/computer-legacy-service.swift"),"-o",join(contents,"MacOS/AIClawComputerUse")],{stdio:"inherit"});
  signMacBundle(source,"com.github.chowyu12.aiclaw.computer-use");
  const client=join(root,"SignedClient");
  execFileSync("xcrun",["swiftc",join(repo,"scripts/fixtures/computer-signed-client.swift"),"-o",client],{stdio:"inherit"});
  signMacBundle(client,"com.github.chowyu12.aiclaw");
  const request=type=>({id:1,requestType:type,request:{}});
  const wrongClient=join(root,"WrongClient");cpSync(client,wrongClient);
  signMacBundle(wrongClient,"com.github.chowyu12.aiclaw.untrusted");
  const idleRoot=join(root,"untrusted");mkdirSync(idleRoot,{mode:0o700});
  const idleSocket=join(idleRoot,"migration.sock"),idleAt=Date.now();
  const idleHelper=start(join(production,"Contents/MacOS/AIClawComputerUse"),["--socket",idleSocket,"--migration-only"]);
  await waitFor(()=>existsSync(idleSocket));
  const nodeRejected=await new Promise((resolve,reject)=>{
    const peer=createConnection(idleSocket);let bytes=0;
    const timer=setTimeout(()=>{peer.destroy();reject(Error("Untrusted client did not get rejected"));},2000);
    peer.on("connect",()=>peer.write(JSON.stringify(request("ComputerUseIPCHandshakeRequest"))+"\n"));
    peer.on("data",data=>{bytes+=data.length;});peer.on("error",()=>{});
    peer.on("close",()=>{clearTimeout(timer);resolve(bytes===0);});
  });
  assert.equal(nodeRejected,true);
  const deniedSignature=spawnSync(wrongClient,[idleSocket,JSON.stringify([request("ComputerUseIPCHandshakeRequest")])],{timeout:3000});
  assert.ok([73,74].includes(deniedSignature.status),"A wrong signed identifier must be disconnected, not left hanging");
  await waitFor(()=>idleHelper.exitCode!==null,12_000);
  assert.equal(idleHelper.exitCode,0,"An unclaimed migration-only helper must terminate normally");
  const unclaimedExitMs=Date.now()-idleAt;
  assert.ok(unclaimedExitMs>=9000&&unclaimedExitMs<12_000,"The production ten-second startup deadline must clean up rejected clients");
  const migrate=async(directory,requests=[request("ComputerUseIPCLegacyMigrationRequest")])=>{
    mkdirSync(directory,{recursive:true,mode:0o700});
    const socket=join(directory,`.migration-${sequence++}.sock`);
    const child=start(join(production,"Contents/MacOS/AIClawComputerUse"),["--socket",socket,"--migration-only"]);
    await waitFor(()=>existsSync(socket)||child.exitCode!==null);
    assert.equal(child.exitCode,null,"The migration-only helper must remain alive until its trusted host disconnects");
    const result=spawnSync(client,[socket,JSON.stringify(requests)],{timeout:15_000,encoding:"utf8"});
    assert.equal(result.status,0,result.stderr);
    await waitFor(()=>child.exitCode!==null);
    return JSON.parse(result.stdout);
  };
  const launchLegacy=async directory=>{
    mkdirSync(directory,{recursive:true,mode:0o700});
    const bundle=join(directory,"AIClaw Computer Use.app");
    cpSync(source,bundle,{recursive:true,verbatimSymlinks:true});
    const socket=join(directory,"legacy.sock");
    // Match a LaunchServices resident's /dev/null stdio. A piped Node stderr
    // is itself an anonymous connected Unix stream and must conservatively
    // keep the migration busy rather than be guessed to be harmless.
    const child=spawn(join(bundle,"Contents/MacOS/AIClawComputerUse"),[socket],{stdio:"ignore"});
    children.push(child);
    await waitFor(()=>existsSync(socket)||child.exitCode!==null);assert.equal(child.exitCode,null);
    assert.equal((await handshake(socket)).result.legacy_fixture,true);
    return {child,bundle,socket};
  };
  const install=join(root,"install");
  const restricted=await migrate(install,[request("ComputerUseIPCHandshakeRequest"),request("ComputerUseIPCListAppsRequest"),request("ComputerUseIPCAppStartCaptureRequest"),request("ComputerUseIPCAppPerformActionRequest")]);
  assert.equal(restricted[0].result.capabilities.migrationOnly,true);assert.deepEqual(restricted[0].result.tools,[]);
  assert.match(restricted[1].error.message,/migration-only/);
  assert.match(restricted[2].error.message,/migration-only/);
  assert.match(restricted[3].error.message,/migration-only/);
  const old=await launchLegacy(install);
  const retired=(await migrate(install))[0];
  assert.ok(!retired.error,JSON.stringify(retired));
  assert.equal(JSON.parse(retired.result.content[0].text).retired_idle_residents,1);
  await waitFor(()=>old.child.exitCode!==null);
  await assert.rejects(handshake(old.socket),error=>["ENOENT","ECONNREFUSED"].includes(error.code));
  await installComputerBundle(production,install);
  const sealed=JSON.parse(await import("node:fs/promises").then(fs=>fs.readFile(join(old.bundle,"Contents/Resources/Build.json"),"utf8")));
  assert.equal(sealed.authenticationProfile,"production");
  const busyRoot=join(root,"busy"),busy=await launchLegacy(busyRoot);
  const held=createConnection(busy.socket);await new Promise((yes,no)=>{held.once("connect",yes);held.once("error",no);});
  await pause(100);
  const refused=(await migrate(busyRoot))[0];
  assert.equal(refused.error?.code,"COMPUTER_LEGACY_BUSY",JSON.stringify(refused));assert.equal(busy.child.exitCode,null);
  held.end();await pause(150);
  assert.equal(JSON.parse((await migrate(busyRoot))[0].result.content[0].text).retired_idle_residents,1);
  await waitFor(()=>busy.child.exitCode!==null);
  const elsewhere=await launchLegacy(join(root,"elsewhere"));
  assert.equal(JSON.parse((await migrate(install))[0].result.content[0].text).retired_idle_residents,0);
  assert.equal(elsewhere.child.exitCode,null);assert.equal((await handshake(elsewhere.socket)).result.legacy_fixture,true);
  const historyRoot=join(root,"history"),history=await launchLegacy(historyRoot);
  rmSync(history.bundle,{recursive:true});cpSync(production,history.bundle,{recursive:true,verbatimSymlinks:true});
  const unknown=(await migrate(historyRoot))[0];
  assert.equal(unknown.error?.code,"COMPUTER_LEGACY_UNVERIFIED",JSON.stringify(unknown));assert.equal(history.child.exitCode,null);
  assert.equal((await handshake(history.socket)).result.legacy_fixture,true);
  console.log(`PASS: production authenticated migration-only mode refused desktop operations; untrusted Node/wrong signature did not claim the one-shot helper (normal exit after ${unclaimedExitMs}ms); pre-replacement same-path signed idle legacy service retired normally and endpoint closed before atomic install; busy accepted FD and other paths preserved; already-unlinked history refused with stable diagnostics. No real user helper or TCC state changed.`);
} finally {
  for(const child of children) if(child.exitCode===null) child.kill("SIGTERM");
  for(const child of children) if(child.exitCode===null) await Promise.race([new Promise(resolve=>child.once("exit",resolve)),pause(2000)]);
  rmSync(root,{recursive:true,force:true});
}
