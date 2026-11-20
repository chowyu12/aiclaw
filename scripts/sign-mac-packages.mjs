/** Sign cross-packaged Mac ZIPs on macOS, retaining the already-signed runtime. */
import { execFileSync } from "node:child_process";
import { mkdtempSync, readdirSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import { signMacApplication, signMacBundle } from "./mac-signing.mjs";
import { fileURLToPath } from "node:url";

const directory=resolve(process.argv[2]);
const names=readdirSync(directory);
const zips=names.filter(name=>/^AIClaw-v.*-mac-(arm64|x64)\.zip$/.test(name));
const cliZips=names.filter(name=>/^AIClaw-CLI-v.*-darwin-(arm64|x64)\.zip$/.test(name));
if (process.platform!=="darwin" || [zips,cliZips].some(files=>!['arm64','x64'].every(arch=>files.filter(name=>name.endsWith(`-${arch}.zip`)).length===1))) {
  throw Error("Signing requires one desktop ZIP and one CLI ZIP for each Mac architecture");
}
for (const name of zips) {
  const temporary=mkdtempSync(join(tmpdir(),"aiclaw-mac-sign-"));
  try {
    execFileSync("/usr/bin/ditto",["-x","-k",join(directory,name),temporary]);
    const architecture=name.endsWith("-arm64.zip")?"arm64":"x64";
    const folder=`AIClaw-darwin-${architecture}`;
    await signMacApplication(join(temporary,folder,"AIClaw.app"));
    if (architecture===process.arch) {
      execFileSync(process.execPath,[fileURLToPath(new URL("./smoke-packaged-computer.mjs",import.meta.url)),join(temporary,folder,"AIClaw.app/Contents/MacOS/AIClaw")],{stdio:"inherit",timeout:90_000});
    }
    // zip -y keeps relative framework symlinks intact.
    rmSync(join(directory,name));
    execFileSync("/usr/bin/zip",["-9qry",join(directory,name),folder],{cwd:temporary});
    console.log(`Signed ${name}`);
  } finally {rmSync(temporary,{recursive:true,force:true});}
}
for (const name of cliZips) {
  const temporary=mkdtempSync(join(tmpdir(),"aiclaw-cli-sign-"));
  try {
    const archive=join(directory,name);
    const binary=join(temporary,"aiclaw");
    execFileSync("/usr/bin/ditto",["-x","-k",archive,temporary]);
    signMacBundle(binary,"com.github.chowyu12.aiclaw.cli");
    const architecture=name.endsWith("-arm64.zip")?"arm64":"x64";
    if (architecture===process.arch) {
      const version=execFileSync(binary,["--version"],{encoding:"utf8"}).trim();
      if (process.env.GITHUB_REF_TYPE==="tag" && `v${version}`!==process.env.GITHUB_REF_NAME) throw Error(`Invalid CLI version: ${version}`);
    }
    rmSync(archive);
    execFileSync("/usr/bin/zip",["-9q",archive,"aiclaw"],{cwd:temporary});
  } finally {rmSync(temporary,{recursive:true,force:true});}
}
