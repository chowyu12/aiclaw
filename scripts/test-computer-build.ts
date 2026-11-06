import test from "node:test";
import assert from "node:assert/strict";
import { mkdtempSync, mkdirSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { computerBuildFingerprint } from "./build-computer-runtime.mjs";

test("native fingerprint ignores host releases and invalidates native, resource, signer and toolchain changes", () => {
  const root=mkdtempSync(join(tmpdir(),"aiclaw-native-inputs-"));
  const files={"package.json":'{"version":"3.7.43"}',"tools/computer-use/runtime.json":'{"version":"1.0.0"}',"tools/computer-use/Sources/Main.swift":"native source","tools/claw-agent/internal/agent/computer-tools.json":"[]","scripts/build-computer-runtime.mjs":"builder","scripts/mac-signing.mjs":"signer"};
  try {
    for(const [name,bytes] of Object.entries(files)) {mkdirSync(dirname(join(root,name)),{recursive:true});writeFileSync(join(root,name),bytes);}
    const input={sourceRoot:root,architectures:["arm64"],certificateSHA256:"fixed-certificate",toolchain:{swift:"swift-1",sdk:"sdk-1",sdkBuild:"build-1"}};
    const first=computerBuildFingerprint(input);
    writeFileSync(join(root,"package.json"),'{"version":"9.9.9"}');
    assert.equal(computerBuildFingerprint(input),first);
    for(const name of Object.keys(files).filter(name=>name!=="package.json")) {
      writeFileSync(join(root,name),files[name as keyof typeof files]+"changed");
      assert.notEqual(computerBuildFingerprint(input),first,name);
      writeFileSync(join(root,name),files[name as keyof typeof files]);
    }
    for(const changed of [{architectures:["arm64","x86_64"]},{certificateSHA256:"other-certificate"},{toolchain:{...input.toolchain,sdkBuild:"build-2"}},{authenticationProfile:"development"},{hostRequirement:"another host"}]) assert.notEqual(computerBuildFingerprint({...input,...changed}),first);
  } finally {rmSync(root,{recursive:true,force:true});}
});
