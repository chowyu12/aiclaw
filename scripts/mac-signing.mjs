/** Persistent local signing identity. Private material never lives in the repo. */
import { createHash, randomBytes, X509Certificate } from "node:crypto";
import { execFileSync } from "node:child_process";
import { chmodSync, closeSync, existsSync, lstatSync, mkdirSync, mkdtempSync, openSync, readFileSync, readSync, realpathSync, rmSync, writeFileSync } from "node:fs";
import { homedir } from "node:os";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const repo = resolve(dirname(fileURLToPath(import.meta.url)), "..");
export const certificatePath = join(repo, "tools/computer-use/signing-certificate.pem");
const directory = () => process.env.AICLAW_MAC_SIGNING_DIRECTORY || (process.env.CI && process.env.RUNNER_TEMP ? join(process.env.RUNNER_TEMP,"aiclaw-signing") : join(homedir(), ".aiclaw/signing"));
function privateDirectory(path) {
  mkdirSync(path, {recursive:true,mode:0o700});
  const entry=lstatSync(path);
  if (!entry.isDirectory() || entry.isSymbolicLink() || entry.uid!==process.getuid() || (entry.mode&0o077)) throw Error("Signing directory must be private, owned and not linked");
}
function privateFile(path) {
  const entry=lstatSync(path);
  if (!entry.isFile() || entry.isSymbolicLink() || entry.uid!==process.getuid() || (entry.mode&0o077)) throw Error("Signing material must be a private owned file");
  return readFileSync(path);
}
function security(args) {
  // Never include commands or their arguments in errors: some contain passwords.
  try { return execFileSync("/usr/bin/security",args,{stdio:["ignore","pipe","pipe"]}); }
  catch { throw Error(`Signing keychain operation failed (${args[0]}); no credentials were logged`); }
}
export function certificateIdentity(pem) {
  const cert=new X509Certificate(pem);
  if (Date.parse(cert.validTo)<=Date.now()) throw Error("Signing certificate expired; do not silently replace the existing identity");
  return {identity:cert.fingerprint.replaceAll(":",""),certificateSHA256:cert.fingerprint256.replaceAll(":","").toLowerCase(),selfSigned:cert.issuer===cert.subject&&cert.verify(cert.publicKey)};
}
export function loadMacSigning() {
  if (process.platform!=="darwin") throw Error("macOS signing requires macOS");
  if (process.env.COMPUTER_USE_SIGN_IDENTITY==="-") return {identity:"-",certificateSHA256:"ad-hoc",selfSigned:false};
  if (process.env.COMPUTER_USE_SIGN_IDENTITY) {
    const keychain=process.env.COMPUTER_USE_SIGN_KEYCHAIN;
    const pem=security(["find-certificate","-a","-c",process.env.COMPUTER_USE_SIGN_IDENTITY,"-p",...(keychain?[keychain]:[])]).toString();
    if ((pem.match(/BEGIN CERTIFICATE/g)||[]).length!==1) throw Error("Signing identity must match exactly one certificate");
    return {...certificateIdentity(pem),keychain};
  }
  const root=directory(); privateDirectory(root);
  const keychain=join(root,"aiclaw.keychain-db");
  if (!existsSync(keychain) || !existsSync(certificatePath)) throw Error("Run node scripts/mac-signing.mjs --setup once, or import the existing release identity; ad-hoc fallback is disabled");
  const password=privateFile(join(root,"password")).toString().trim();
  const identity=certificateIdentity(readFileSync(certificatePath));
  privateFile(keychain);
  security(["unlock-keychain","-p",password,keychain]);
  const pem=security(["find-certificate","-a","-p",keychain]).toString();
  if ((pem.match(/BEGIN CERTIFICATE/g)||[]).length!==1 || certificateIdentity(pem).certificateSHA256!==identity.certificateSHA256) throw Error("Local signing certificate does not match the pinned public certificate");
  return {...identity,keychain};
}
export function setupMacSigning() {
  if (process.platform!=="darwin") throw Error("macOS signing requires macOS");
  const root=directory(); privateDirectory(root);
  const p12=join(root,"identity.p12"), passwordPath=join(root,"password"), keychain=join(root,"aiclaw.keychain-db");
  if (!existsSync(p12)) {
    if (existsSync(passwordPath) || existsSync(keychain)) throw Error("Incomplete signing identity; preserve existing material rather than generating a new key");
    const imported=process.env.AICLAW_MAC_SIGNING_P12_BASE64;
    if (!imported && (process.env.CI || existsSync(certificatePath))) throw Error("The fixed signing identity is missing; import its P12 and password instead of generating a replacement");
    const password=imported?process.env.AICLAW_MAC_SIGNING_PASSWORD:randomBytes(32).toString("base64");
    if (!password) throw Error("The signing P12 password is required");
    writeFileSync(passwordPath,password,{mode:0o600,flag:"wx"});
    if (imported) writeFileSync(p12,Buffer.from(imported,"base64"),{mode:0o600,flag:"wx"});
    else {
      const temporary=mkdtempSync(join(root,".generate-"));
      try {
        const config=join(temporary,"openssl.cnf"), key=join(temporary,"key.pem"), cert=join(temporary,"certificate.pem");
        writeFileSync(config,"[req]\ndistinguished_name=dn\nx509_extensions=extensions\nprompt=no\n[dn]\nCN=AIClaw Code Signing\n[extensions]\nbasicConstraints=critical,CA:FALSE\nkeyUsage=critical,digitalSignature\nextendedKeyUsage=critical,codeSigning\n",{mode:0o600});
        execFileSync("/usr/bin/openssl",["req","-x509","-newkey","rsa:3072","-nodes","-days","3650","-config",config,"-keyout",key,"-out",cert],{stdio:"ignore"});
        writeFileSync(p12,"",{mode:0o600,flag:"wx"});
        execFileSync("/usr/bin/openssl",["pkcs12","-export","-inkey",key,"-in",cert,"-out",p12,"-passout",`file:${passwordPath}`],{stdio:"ignore"});
      } finally {rmSync(temporary,{recursive:true,force:true});}
    }
  }
  const password=privateFile(passwordPath).toString().trim(); privateFile(p12);
  let pem;
  try {pem=execFileSync("/usr/bin/openssl",["pkcs12","-in",p12,"-passin",`file:${passwordPath}`,"-clcerts","-nokeys"],{stdio:["ignore","pipe","pipe"]});}
  catch {throw Error("Could not read the existing signing identity; its files were preserved");}
  const identity=certificateIdentity(pem);
  if (existsSync(certificatePath)) {
    if (certificateIdentity(readFileSync(certificatePath)).certificateSHA256!==identity.certificateSHA256) throw Error("Imported identity differs from the pinned release certificate");
  } else writeFileSync(certificatePath,new X509Certificate(pem).toString().trimEnd()+"\n",{flag:"wx"});
  if (!existsSync(keychain)) {
    security(["create-keychain","-p",password,keychain]);
    chmodSync(keychain,0o600);
  }
  security(["unlock-keychain","-p",password,keychain]);
  security(["set-keychain-settings","-lut","21600",keychain]);
  // Only import once; subsequent setup runs keep the exact same key.
  const present=security(["find-certificate","-a","-p",keychain]).toString();
  if (!present.includes("BEGIN CERTIFICATE")) security(["import",p12,"-k",keychain,"-P",password,"-T","/usr/bin/codesign"]);
  security(["set-key-partition-list","-S","apple-tool:,apple:,codesign:","-s","-k",password,keychain]);
  // codesign also consults the search list when assembling certificate chains.
  // Change only the disposable GitHub runner, never the developer's search list.
  if (process.env.GITHUB_ACTIONS==="true" && process.env.RUNNER_TEMP && root===join(process.env.RUNNER_TEMP,"aiclaw-signing")) {
    const existing=[...security(["list-keychains","-d","user"]).toString().matchAll(/"([^"\n]+)"/g)].map(match=>match[1]);
    security(["list-keychains","-d","user","-s",keychain,...existing.filter(path=>path!==keychain)]);
  }
  console.log(`Signing identity ready: ${identity.certificateSHA256}. Private material remains in the local signing directory.`);
  return loadMacSigning();
}
export function signMacBundle(bundle, identifier, signing=loadMacSigning()) {
  execFileSync("/usr/bin/codesign",["--force","--sign",signing.identity,...(signing.keychain?["--keychain",signing.keychain]:[]),"--identifier",identifier,...(signing.selfSigned?["--timestamp=none"]:signing.identity==="-"?[]:["--options","runtime","--timestamp"]),bundle],{stdio:"inherit"});
  verifyMacBundle(bundle,identifier,signing);
}
export function verifyMacBundle(bundle, identifier, signing=loadMacSigning()) {
  execFileSync("/usr/bin/codesign",["--verify","--strict",...(signing.identity==="-"?[]:["-R",`=identifier "${identifier}" and certificate leaf = H"${signing.identity}"`]),bundle],{stdio:"pipe"});
}
export async function signMacApplication(app, signing=loadMacSigning()) {
  const {sign}=await import("@electron/osx-sign");
  const helper=join(app,"Contents/Resources/app/computer-use/AIClaw Computer Use.app");
  const binary=join(helper,"Contents/MacOS/AIClawComputerUse");
  const before=existsSync(binary)?createHash("sha256").update(readFileSync(binary)).digest("hex"):undefined;
  if (before) verifyMacBundle(helper,"com.github.chowyu12.aiclaw.computer-use",signing);
  await sign({app,identity:signing.identity,keychain:signing.keychain,identityValidation:false,
    timestamp:signing.selfSigned?"none":undefined,preAutoEntitlements:false,preEmbedProvisioningProfile:false,
    ignore:file=>{
      if (file.includes("/computer-use/AIClaw Computer Use.app")) return true;
      if (!lstatSync(file).isFile()) return false;
      const fd=openSync(file,"r"), magic=Buffer.alloc(4);
      try {readSync(fd,magic,0,4,0);} finally {closeSync(fd);}
      return !["feedface","feedfacf","cefaedfe","cffaedfe","cafebabe","bebafeca","cafebabf","bfbafeca"].includes(magic.toString("hex"));
    },
    optionsForFile:()=>({hardenedRuntime:!signing.selfSigned,entitlements:join(repo,"tools/computer-use/electron-entitlements.plist")})});
  verifyMacBundle(app,"com.github.chowyu12.aiclaw",signing);
  if (before && createHash("sha256").update(readFileSync(binary)).digest("hex")!==before) throw Error("Main app signing modified the cached Computer Use signature");
}
if (process.argv[1] && realpathSync(process.argv[1])===fileURLToPath(import.meta.url)) {
  if (process.argv.includes("--setup")) setupMacSigning();
  else if (process.argv.includes("--fingerprint")) console.log(loadMacSigning().certificateSHA256);
  else throw Error("Use --setup or --fingerprint");
}
