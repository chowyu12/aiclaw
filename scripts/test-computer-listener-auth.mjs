/** Read-only real listener identity, FD inheritance and forged server acceptance. */
import assert from 'node:assert/strict';
import {spawn, spawnSync, execFileSync} from 'node:child_process';
import {createHash} from 'node:crypto';
import {cpSync,existsSync,mkdtempSync,readFileSync,rmSync} from 'node:fs';
import {createServer} from 'node:net';
import {join} from 'node:path';
import {setTimeout as pause} from 'node:timers/promises';
import {buildComputerRuntime} from './build-computer-runtime.mjs';
import {loadMacSigning,signMacBundle} from './mac-signing.mjs';
import {ComputerNativeRuntime} from '../apps/desktop/src/main/computer-runtime.ts';
if(process.platform!=='darwin')process.exit(0);
const root=mkdtempSync('/tmp/aiclaw-listener-'); let helper,server; const children=[];
const execute=(binary,args)=>new Promise((resolve,reject)=>{const child=spawn(binary,args,{stdio:'ignore'});children.push(child);child.once('error',reject);child.once('exit',code=>resolve(code));});
try {
 const bundle=buildComputerRuntime({universal:true,output:join(process.cwd(),'build/listener-auth-production')});
 const executable=join(bundle,'Contents/MacOS/AIClawComputerUse'),identity=createHash('sha256').update(readFileSync(executable)).digest('hex');
 const client=join(root,'SignedClient');execFileSync('xcrun',['swiftc','scripts/fixtures/computer-listener-client.swift','-o',client],{stdio:'inherit'});signMacBundle(client,'com.github.chowyu12.aiclaw',loadMacSigning());
 let frames=0;
 const fake=join(root,'fake.sock');server=createServer(socket=>socket.on('data',()=>{frames++;socket.write(JSON.stringify({id:1,result:{protocolVersion:'AIClawComputerUseIPC-1',serverVersion:'1',tools:[],processExecutableSHA256:identity}})+'\n');}));
 await new Promise(resolve=>server.listen(fake,resolve));
 assert.equal(await execute(client,[fake,executable,identity,'unchecked']),0,'pre-fix signed host accepts the fake public artifact hash');
 assert.equal(frames,1);frames=0;
 assert.equal(await execute(client,[fake,executable,identity,'verify']),77,'signed host must reject the fake kernel peer');assert.equal(frames,0);
 await assert.rejects(ComputerNativeRuntime.connectBundle(bundle,fake),/could not be verified/);assert.equal(frames,0,'Node connect/FD verifier sends zero application frames');
 await assert.rejects(ComputerNativeRuntime.verifyListener({},executable,identity),/unavailable/,'unsupported socket FD access fails closed');
 const endpoint=join(root,'real.sock');helper=spawn(executable,['--socket',endpoint],{stdio:'ignore'});
 for(let i=0;i<100&&!existsSync(endpoint);i++)await pause(50);assert.ok(existsSync(endpoint));
 assert.equal(await execute(client,[endpoint,executable,identity,'verify']),0,'fixed-certificate host retains original client audit identity when its verifier inherits FD');
 assert.equal(await execute(client,[endpoint,executable,'0'.repeat(64),'verify']),77,'wrong full-artifact hash rejected before a frame');
 const rosetta=process.arch==='arm64'&&spawnSync('/usr/bin/arch',['-x86_64','/usr/bin/true']).status===0;
 if(rosetta)assert.equal(await execute(client,[endpoint,executable,identity,'verify-x86_64']),0,'x86_64 verifier accepts arm64 peer from the same verified universal artifact');
 const copied=join(root,'copied.app');cpSync(bundle,copied,{recursive:true});const alternate=join(root,'alternate.sock');
 const copiedHelper=spawn(join(copied,'Contents/MacOS/AIClawComputerUse'),['--socket',alternate],{stdio:'ignore'});children.push(copiedHelper);
 for(let i=0;i<100&&!existsSync(alternate);i++)await pause(50);assert.ok(existsSync(alternate));
 assert.equal(await execute(client,[alternate,executable,identity,'verify']),77,'same signed artifact running from a different path is refused');
 console.log(JSON.stringify({passed:true,signed_host_fake_server_poc:true,fake_server_frames_after_fix:frames,node_fd_inheritance:true,production_host_real_listener:true,universal_artifact_verified:true,rosetta_x64_verifier_arm64_server:rosetta?'passed':'not executable on this host',same_signed_artifact_wrong_path_refused:true,no_permissions_screenshots_or_input:true}));
} finally {
 for(const child of [...children,helper].filter(Boolean))if(child.exitCode===null){child.kill();await Promise.race([new Promise(resolve=>child.once('exit',resolve)),pause(2000)]);}
 if(server)await new Promise(resolve=>server.close(resolve));rmSync(root,{recursive:true,force:true});
}
