/** Real rebuild/identity acceptance; does not inspect UI or change TCC grants. */
import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { execFileSync, spawnSync } from "node:child_process";
import { cpSync, existsSync, mkdirSync, mkdtempSync, readFileSync, rmSync, statSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { loadMacSigning } from "./mac-signing.mjs";
if (process.env.COMPUTER_USE_SIGN_IDENTITY==="-") {console.log("Fixed identity acceptance skipped in explicit ad-hoc mode");process.exit(0);}
const repo=resolve(dirname(fileURLToPath(import.meta.url)),"..");
const root=mkdtempSync(join(tmpdir(),"aiclaw-signing-acceptance-"));
try {
  for(const path of ["tools/computer-use","scripts","tools/claw-agent/internal/agent"]) mkdirSync(join(root,path),{recursive:true});
  for(const path of ["tools/computer-use/Sources","tools/computer-use/runtime.json","tools/computer-use/signing-certificate.pem","tools/claw-agent/internal/agent/computer-tools.json","scripts/build-computer-runtime.mjs","scripts/mac-signing.mjs"]) cpSync(join(repo,path),join(root,path),{recursive:true,verbatimSymlinks:true});
  writeFileSync(join(root,"package.json"),'{"version":"3.7.43"}');
  const build=()=>execFileSync(process.execPath,[join(root,"scripts/build-computer-runtime.mjs")],{encoding:"utf8",stdio:["ignore","pipe","pipe"]});
  const bundle=join(root,"apps/desktop/computer-use/AIClaw Computer Use.app");
  const binary=join(bundle,"Contents/MacOS/AIClawComputerUse");
  const digest=path=>createHash("sha256").update(readFileSync(path)).digest("hex");
  const requirement=path=>{
    const result=spawnSync("/usr/bin/codesign",["-d","-r-",path],{encoding:"utf8"});assert.equal(result.status,0);
    return (result.stdout+"\n"+result.stderr).match(/^designated => (.+)$/m)[1];
  };
  build(); const first=digest(binary), inode=statSync(binary).ino;
  const old=join(root,"previous.app");cpSync(bundle,old,{recursive:true,verbatimSymlinks:true});
  writeFileSync(join(root,"package.json"),'{"version":"9.9.9"}');
  assert.match(build(),/Reused unchanged/);assert.equal(digest(binary),first);assert.equal(statSync(binary).ino,inode);
  const config=join(root,"tools/computer-use/runtime.json");
  writeFileSync(config,JSON.stringify({version:"1.0.1"}));
  assert.match(build(),/Built /);assert.notEqual(digest(binary),first);
  const dr=requirement(bundle);assert.equal(dr,requirement(old));assert.match(dr,/certificate leaf/);assert.ok(!dr.includes("cdhash"));
  for(const app of [bundle,old]) execFileSync("/usr/bin/codesign",["--verify","--strict","-R",`=${dr}`,app]);
  writeFileSync(join(bundle,"Contents/Resources/Build.json"),'{"fingerprint":"tampered"}');
  assert.throws(build,/Command failed/);
  const empty=join(root,"empty-signing");
  const missing=spawnSync(process.execPath,[join(root,"scripts/mac-signing.mjs"),"--setup"],{encoding:"utf8",env:{...process.env,CI:"true",AICLAW_MAC_SIGNING_DIRECTORY:empty,AICLAW_MAC_SIGNING_P12_BASE64:"",AICLAW_MAC_SIGNING_PASSWORD:""}});
  assert.notEqual(missing.status,0);assert.match(missing.stderr,/fixed signing identity is missing/);assert.ok(!existsSync(join(empty,"identity.p12")));
  const signing=loadMacSigning(), source=signing.keychain&&dirname(signing.keychain);
  if (source && existsSync(join(source,"identity.p12"))) {
    const imported=spawnSync(process.execPath,[join(root,"scripts/mac-signing.mjs"),"--setup"],{encoding:"utf8",env:{...process.env,CI:"true",AICLAW_MAC_SIGNING_DIRECTORY:join(root,"ci-signing"),AICLAW_MAC_SIGNING_P12_BASE64:readFileSync(join(source,"identity.p12")).toString("base64"),AICLAW_MAC_SIGNING_PASSWORD:readFileSync(join(source,"password"),"utf8").trim()}});
    assert.equal(imported.status,0,imported.stderr);assert.ok(imported.stdout.includes(signing.certificateSHA256));
  }
  console.log("PASS: host-only release reused exact signed bytes; changed helper rebuilt with identical certificate DR; CI import retained the pinned identity; tampered cache and missing CI identity refused; no TCC grants changed");
} finally {
  const keychain=join(root,"ci-signing/aiclaw.keychain-db");
  if (existsSync(keychain)) spawnSync("/usr/bin/security",["delete-keychain",keychain]);
  rmSync(root,{recursive:true,force:true});
}
