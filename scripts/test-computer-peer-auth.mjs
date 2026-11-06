/** Real kernel-peer/code-signature authorization, without screenshots or input. */
import assert from "node:assert/strict";
import { spawn, spawnSync, execFileSync } from "node:child_process";
import { existsSync, mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { createConnection } from "node:net";
import { join } from "node:path";
import { setTimeout as pause } from "node:timers/promises";
import { buildComputerRuntime } from "./build-computer-runtime.mjs";
import { loadMacSigning, signMacBundle } from "./mac-signing.mjs";

if (process.platform !== "darwin") { console.log("Native client authentication acceptance requires macOS"); process.exit(0); }
const root=mkdtempSync("/tmp/aiclaw-peer-");
let helper;
try {
  const bundle=buildComputerRuntime({output:join(root,"runtime")});
  const socket=join(root,"native.sock");
  helper=spawn(join(bundle,"Contents/MacOS/AIClawComputerUse"),["--socket",socket],{stdio:["ignore","ignore","pipe"]});
  let errors="";helper.stderr.on("data",chunk=>{errors+=chunk;});
  for(let attempt=0;attempt<100&&!existsSync(socket);attempt++) {
    if(helper.exitCode!==null) throw Error(`Native fixture exited: ${errors}`);
    await pause(50);
  }
  assert.ok(existsSync(socket),`Native fixture did not listen: ${errors}`);
  // A same-user Node process supplies a forged identity field. The server must
  // close before parsing any request or returning even read-only handshake data.
  const denied=await new Promise((resolve,reject)=>{
    const client=createConnection(socket);let bytes=0;
    const timer=setTimeout(()=>{client.destroy();reject(Error("Untrusted client was not promptly rejected"));},3000);
    client.on("connect",()=>client.write(JSON.stringify({id:1,requestType:"ComputerUseIPCHandshakeRequest",request:{sender:"com.github.chowyu12.aiclaw"}})+"\n"));
    client.on("data",chunk=>{bytes+=chunk.length;});
    client.on("error",()=>{});
    client.on("close",()=>{clearTimeout(timer);resolve(bytes===0);});
  });
  assert.equal(denied,true,"An unsigned same-user client must receive no response");
  const source=join(root,"Client.swift"),binary=join(root,"Client");
  writeFileSync(source,`import Darwin
import Foundation
signal(SIGPIPE, SIG_IGN)
let descriptor = socket(AF_UNIX, SOCK_STREAM, 0)
var address = sockaddr_un()
address.sun_family = sa_family_t(AF_UNIX)
address.sun_len = UInt8(MemoryLayout<sockaddr_un>.size)
withUnsafeMutableBytes(of: &address.sun_path) { raw in
    raw.initializeMemory(as: UInt8.self, repeating: 0)
    raw.copyBytes(from: Array(CommandLine.arguments[1].utf8) + [0])
}
let connected = withUnsafePointer(to: &address) { pointer in
    pointer.withMemoryRebound(to: sockaddr.self, capacity: 1) { connect(descriptor, $0, socklen_t(MemoryLayout<sockaddr_un>.size)) }
}
guard connected == 0 else { exit(72) }
var timeout = timeval(tv_sec: 3, tv_usec: 0)
setsockopt(descriptor, SOL_SOCKET, SO_RCVTIMEO, &timeout, socklen_t(MemoryLayout<timeval>.size))
let request = Array("{\\"id\\":1,\\"requestType\\":\\"ComputerUseIPCHandshakeRequest\\",\\"request\\":{}}\\n".utf8)
_ = request.withUnsafeBytes { send(descriptor, $0.baseAddress, $0.count, 0) }
var data = Data(), buffer = [UInt8](repeating: 0, count: 65536)
while !data.contains(10) {
    let count = recv(descriptor, &buffer, buffer.count, 0)
    if count <= 0 { exit(73) }
    data.append(contentsOf: buffer.prefix(count))
}
close(descriptor)
guard let frame = try JSONSerialization.jsonObject(with: data) as? [String: Any],
      let result = frame["result"] as? [String: Any],
      result["protocolVersion"] as? String == "AIClawComputerUseIPC-1" else { exit(74) }
print("Authenticated native handshake accepted")
`);
  execFileSync("xcrun",["swiftc",source,"-o",binary],{stdio:"inherit"});
  const signing=loadMacSigning();
  signMacBundle(binary,"com.github.chowyu12.aiclaw.untrusted",signing);
  assert.equal(spawnSync(binary,[socket],{timeout:5000}).status,73,"A signed different identifier must be rejected");
  execFileSync("/usr/bin/codesign",["--force","--sign","-","--identifier","com.github.chowyu12.aiclaw",binary],{stdio:"inherit"});
  assert.equal(spawnSync(binary,[socket],{timeout:5000}).status,73,"The correct identifier without the pinned certificate must be rejected");
  signMacBundle(binary,"com.github.chowyu12.aiclaw",signing);
  const accepted=spawnSync(binary,[socket],{timeout:5000,encoding:"utf8"});
  assert.equal(accepted.status,0,accepted.stderr);
  assert.match(accepted.stdout,/Authenticated native handshake accepted/);
  console.log("PASS: production helper rejected same-user Node/forged identity, wrong signed identifier and ad-hoc identity spoof; actual fixed-certificate host passed kernel audit-token authorization. No permissions, screenshots or input used.");
} finally {
  if(helper&&helper.exitCode===null) {
    helper.kill();await Promise.race([new Promise(resolve=>helper.once("exit",resolve)),pause(3000)]);
    if(helper.exitCode===null) helper.kill("SIGKILL");
  }
  rmSync(root,{recursive:true,force:true});
}
