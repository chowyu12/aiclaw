/** Only copied into the isolated acceptance package; never the release entry. */
const {app}=require('electron');
const assert=require('node:assert/strict');
const {createServer,createConnection}=require('node:net');
const {readFile}=require('node:fs/promises');
const {createHash}=require('node:crypto');
const {homedir}=require('node:os');
const {join}=require('node:path');
const {pathToFileURL}=require('node:url');
let runtime,server;
app.whenReady().then(async()=>{
 assert.notEqual(process.defaultApp,true,'a real packaged host must use production');
 assert.equal(homedir(),process.env.AICLAW_ACCEPTANCE_HOME,'the acceptance must remain inside its private HOME');
 const native=await import(pathToFileURL(join(__dirname,'dist/main/computer-runtime.js')));
 runtime=await native.ComputerNativeRuntime.create();
 const permissions=JSON.parse((await runtime.call('permission_status',{})).content[0].text);
 assert.equal(permissions.setupWindowShown,false);
 const installed=native.computerBundle();assert.ok(installed.startsWith(homedir()+'/'));
 await runtime.close();runtime=undefined;
 await new Promise((resolve,reject)=>{const socket=createConnection(process.env.AICLAW_ACCEPTANCE_LEGACY_SOCKET);socket.on('connect',()=>{socket.destroy();reject(Error('legacy endpoint still accepts connections'))});socket.on('error',error=>{if(['ENOENT','ECONNREFUSED'].includes(error.code))resolve();else reject(error)});});
 const hash=createHash('sha256').update(await readFile(native.computerExecutable(installed))).digest('hex');let frames=0;
 const endpoint=join(homedir(),'fake.sock');
 server=createServer(socket=>socket.on('data',()=>{frames++;socket.write(JSON.stringify({id:1,result:{protocolVersion:native.COMPUTER_PROTOCOL,serverVersion:'1',tools:[],processExecutableSHA256:hash}})+'\n');}));
 await new Promise(resolve=>server.listen(endpoint,resolve));
 await assert.rejects(native.ComputerNativeRuntime.connectBundle(installed,endpoint),/could not be verified/);assert.equal(frames,0);
 console.log(JSON.stringify({passed:true,appPackaged:app.isPackaged,defaultApp:process.defaultApp??false,electron:process.versions.electron,installed,production_peer_authentication:true,production_listener_authentication:true,old_endpoint_closed_before_install:true,fake_frames:frames,permissions,setup_not_requested:true,ui_shown:false}));
}).then(()=>finish(0)).catch(error=>{console.error(error);return finish(1)});
async function finish(code){try{await runtime?.close();if(server)await new Promise(resolve=>server.close(resolve));}finally{app.exit(code)}}
