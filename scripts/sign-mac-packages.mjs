/** Sign cross-packaged Mac ZIPs on macOS, retaining the already-signed runtime. */
import { execFileSync } from "node:child_process";
import { mkdtempSync, readdirSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import { signMacApplication } from "./mac-signing.mjs";
import { fileURLToPath } from "node:url";

const directory=resolve(process.argv[2]);
const zips=readdirSync(directory).filter(name=>/^AIClaw-.*-mac-(arm64|x64)\.zip$/.test(name));
if (process.platform!=="darwin" || zips.length!==2) throw Error("Signing requires macOS and exactly two Mac release ZIPs");
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
    execFileSync("/usr/bin/zip",["-qry",join(directory,name),folder],{cwd:temporary});
    console.log(`Signed ${name}`);
  } finally {rmSync(temporary,{recursive:true,force:true});}
}
