/** Opt-in macOS production signed full Electron bundle, isolated HOME.
 * Copies the complete app resources but uses a temporary read-only fixture
 * entry instead of starting the release's normal window/UI. This exercises
 * production packaged identity, mutual IPC authentication and real pre-install
 * migration; it does not assert default UI startup or Apple notarization.
 * Does not build shared dist/helper. Run only after dist is fully compiled.
 */
import assert from 'node:assert/strict';
import {execFileSync,spawn} from 'node:child_process';
import {cpSync,existsSync,mkdirSync,mkdtempSync,readFileSync,realpathSync,writeFileSync} from 'node:fs';
import {join,resolve} from 'node:path';
import {createConnection} from 'node:net';
import {setTimeout as pause} from 'node:timers/promises';
import {packager} from '@electron/packager';
import {buildComputerRuntime} from './build-computer-runtime.mjs';
import {stageComputerHostDependencies} from './stage-computer-runtime.mjs';
import {signMacApplication,signMacBundle,verifyMacBundle,loadMacSigning} from './mac-signing.mjs';
if(process.platform!=='darwin'){console.log('Production packaged authentication requires macOS');process.exit(0)}
const repo=resolve('.'),root=mkdtempSync('/tmp/acp-'),stage=join(root,'stage'),home=join(root,'home'),directory=join(home,'.aiclaw/computer-use');
let legacy,host;const signing=loadMacSigning();
const waitFor=async predicate=>{for(let i=0;i<150;i++){if(predicate())return;await pause(50)}throw Error('Temporary acceptance process did not become ready')};
try{
 mkdirSync(stage);mkdirSync(directory,{recursive:true,mode:0o700});
 // Snapshot shared compilation once. This script never compiles or replaces it.
 cpSync(join(repo,'apps/desktop/dist'),join(stage,'dist'),{recursive:true});
 cpSync(join(repo,'apps/desktop/assets'),join(stage,'assets'),{recursive:true});
 for(const file of ['LICENSE','LICENSE_CN.md'])cpSync(join(repo,file),join(stage,file));
 const client=join(stage,'node_modules/@aiclaw/agent-client');mkdirSync(client,{recursive:true});
 cpSync(join(repo,'packages/agent-client/dist'),join(client,'dist'),{recursive:true});
 cpSync(join(repo,'packages/agent-client/package.json'),join(client,'package.json'));
 await stageComputerHostDependencies({repo,destination:stage});
 const helper=buildComputerRuntime({universal:true,output:join(repo,'build/listener-auth-production')});
 mkdirSync(join(stage,'computer-use'));cpSync(helper,join(stage,'computer-use/AIClaw Computer Use.app'),{recursive:true});
 const metadata=JSON.parse(readFileSync(join(helper,'Contents/Resources/Build.json'),'utf8'));assert.equal(metadata.authenticationProfile,'production');
 cpSync(join(repo,'scripts/fixtures/computer-packaged-auth.cjs'),join(stage,'acceptance.cjs'));
 const version=JSON.parse(readFileSync(join(repo,'package.json'),'utf8')).version;
 writeFileSync(join(stage,'package.json'),JSON.stringify({name:'aiclaw',productName:'AIClaw',version,type:'module',main:'acceptance.cjs'}));
 const paths=await packager({dir:stage,out:join(root,'package'),electronVersion:JSON.parse(readFileSync(join(repo,'node_modules/electron/package.json'),'utf8')).version,platform:'darwin',arch:process.arch,name:'AIClaw',appVersion:version,appBundleId:'com.github.chowyu12.aiclaw',overwrite:true,prune:false,asar:false,icon:join(repo,'apps/desktop/assets/icon.icns'),extraResource:[join(repo,'tools/claw-agent/claw-agent'),join(repo,'apps/browser-extension')],extendInfo:{LSUIElement:true}});
 const application=join(paths[0],'AIClaw.app');await signMacApplication(application,signing);verifyMacBundle(application,'com.github.chowyu12.aiclaw',signing);
 // Seed a signed no-auth older generation at precisely this private HOME path.
 const old=join(directory,'AIClaw Computer Use.app'),contents=join(old,'Contents');mkdirSync(join(contents,'MacOS'),{recursive:true});
 writeFileSync(join(contents,'Info.plist'),'<?xml version="1.0"?><plist version="1.0"><dict><key>CFBundleIdentifier</key><string>com.github.chowyu12.aiclaw.computer-use</string><key>CFBundleExecutable</key><string>AIClawComputerUse</string><key>CFBundleName</key><string>Legacy Acceptance Fixture</string><key>CFBundlePackageType</key><string>APPL</string><key>LSUIElement</key><true/></dict></plist>');
 const oldExecutable=join(contents,'MacOS/AIClawComputerUse');execFileSync('xcrun',['swiftc',join(repo,'scripts/fixtures/computer-legacy-service.swift'),'-o',oldExecutable],{stdio:'inherit'});signMacBundle(old,'com.github.chowyu12.aiclaw.computer-use',signing);
 const oldSocket=join(directory,'legacy.sock');legacy=spawn(oldExecutable,[oldSocket],{stdio:'ignore'});await waitFor(()=>existsSync(oldSocket));
 const legacyProof=await new Promise((resolve,reject)=>{const socket=createConnection(oldSocket);socket.on('connect',()=>socket.write('{}\n'));socket.on('data',bytes=>{socket.end();resolve(JSON.parse(String(bytes)))});socket.on('error',reject)});assert.equal(legacyProof.result.legacy_fixture,true);await pause(100);
 const output=[];host=spawn(join(application,'Contents/MacOS/AIClaw'),[],{env:{...process.env,HOME:home,CFFIXED_USER_HOME:home,AICLAW_ACCEPTANCE_HOME:home,AICLAW_ACCEPTANCE_LEGACY_SOCKET:oldSocket},stdio:['ignore','pipe','pipe']});
 host.stdout.on('data',chunk=>output.push(String(chunk)));host.stderr.on('data',chunk=>output.push(String(chunk)));
 const status=await Promise.race([new Promise(resolve=>host.once('exit',resolve)),pause(30_000,undefined,{ref:false}).then(()=>{throw Error('Packaged acceptance timed out')})]);
 assert.equal(status,0,output.join(''));assert.equal(legacy.exitCode,0,'production packaged migration must normally terminate its own old idle helper');
 console.log(output.join('').trim());console.log(JSON.stringify({package:application,full_electron_bundle:true,temporary_acceptance_entry:true,release_entry_unchanged:true,fixed_certificate_verified:true,old_signed_idle_generation_retired:true,real_user_home_untouched:true}));
}finally{
 // Only exact executables inside this fresh private HOME qualify for cleanup.
 // No bundle-ID kill, no production root, no force-kill, no live socket deletion.
 for(const child of [host,legacy].filter(Boolean))if(child.exitCode===null){child.kill();await Promise.race([new Promise(resolve=>child.once('exit',resolve)),pause(2000)])}
 const installedExecutable=join(directory,'AIClaw Computer Use.app/Contents/MacOS/AIClawComputerUse');
 const expected=existsSync(installedExecutable)?realpathSync(installedExecutable):"";
 const rows=execFileSync('/bin/ps',['-axo','pid=,comm='],{encoding:'utf8'}).split('\n');
 for(const row of rows){const match=row.trim().match(/^(\d+)\s+(.+)$/);if(!match||match[2]!==expected)continue;const pid=Number(match[1]);try{if(execFileSync('/bin/ps',['-p',String(pid),'-o','comm='],{encoding:'utf8'}).trim()===expected)process.kill(pid,'SIGTERM')}catch{}}
 // Preserve the signed reviewable artifact and receipt at this printed path.
 console.log(`Isolated packaged acceptance artifacts: ${root}`);
}
