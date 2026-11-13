/** Exercise invalid-cache recovery on a disposable bundle; never modify the installed app. */
import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import { cpSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import { buildComputerRuntime, computerBuildConfiguration, COMPUTER_BUNDLE_NAME } from "./build-computer-runtime.mjs";
if (process.platform === "darwin") {
 const temporary=mkdtempSync(join(tmpdir(),"aiclaw-cache-recovery-"));
 try {
  buildComputerRuntime({development:true});
  cpSync(resolve("apps/desktop/computer-use-development"),temporary,{recursive:true});
  const bundle=join(temporary,COMPUTER_BUNDLE_NAME);
  execFileSync("/usr/bin/codesign",["--remove-signature",bundle]);
  assert.equal(buildComputerRuntime({development:true,output:temporary}),bundle);
  execFileSync("/usr/bin/codesign",["--verify","--strict",bundle]);
  assert.equal(buildComputerRuntime({development:true,output:temporary}),bundle);
  const metadata=join(temporary,"AIClaw Computer Use.build.json");
  const data=JSON.parse(readFileSync(metadata,"utf8"));
  data.fingerprint=computerBuildConfiguration(false,false).fingerprint;
  writeFileSync(metadata,JSON.stringify(data));
  execFileSync("/usr/bin/codesign",["--remove-signature",bundle]);
  assert.throws(()=>buildComputerRuntime({output:temporary}),/codesign|not signed/);
  console.log("PASS: development rebuilt and re-signed invalid cache; production rejected it.");
 } finally {rmSync(temporary,{recursive:true,force:true});}
}
