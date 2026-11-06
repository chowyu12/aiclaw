/** Read-only Electron FD inheritance acceptance; never asks for permissions. */
const {app}=require('electron');
const assert=require('node:assert/strict');
const {createServer}=require('node:net');
const {mkdtemp,readFile,rm}=require('node:fs/promises');
const {createHash}=require('node:crypto');
const {tmpdir}=require('node:os');
const {join}=require('node:path');
const {pathToFileURL}=require('node:url');
let runtime,server,root;
app.whenReady().then(async()=>{
 if(process.platform!=="darwin"){console.log("macOS listener authentication acceptance skipped on this platform");return;}
 const native=await import(pathToFileURL(join(__dirname,'../apps/desktop/dist/main/computer-runtime.js')));
 runtime=await native.ComputerNativeRuntime.create();
 const permissions=JSON.parse((await runtime.call('permission_status',{})).content[0].text);
 await runtime.close();runtime=undefined;
 root=await mkdtemp(join(tmpdir(),'aiclaw-listener-electron-'));
 const endpoint=join(root,'fake.sock'),bundle=native.computerBundle();
 const hash=createHash('sha256').update(await readFile(native.computerExecutable(bundle))).digest('hex');let frames=0;
 server=createServer(socket=>socket.on('data',()=>{frames++;socket.write(JSON.stringify({id:1,result:{protocolVersion:native.COMPUTER_PROTOCOL,serverVersion:'1',tools:[],processExecutableSHA256:hash}})+'\n');}));
 await new Promise(resolve=>server.listen(endpoint,resolve));
 await assert.rejects(native.ComputerNativeRuntime.connectBundle(bundle,endpoint),/could not be verified/);
 assert.equal(frames,0);
 console.log(JSON.stringify({passed:true,electron:process.versions.electron,node:process.versions.node,real_listener_verified:true,fake_listener_frames:frames,permissions,setup_not_requested:true}));
}).then(()=>finish(0)).catch(error=>{console.error(error);return finish(1)});
async function finish(code){try{await runtime?.close();if(server)await new Promise(resolve=>server.close(resolve));if(root)await rm(root,{recursive:true,force:true});}finally{app.exit(code)}}
