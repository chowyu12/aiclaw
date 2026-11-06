/** Isolated Linux/X11 only. Compares Electron DIP bounds against actual X11 pixels. */
const { app, BrowserWindow, screen } = require('electron');
const { spawn, execFileSync } = require('node:child_process');
const { once } = require('node:events');
const { join } = require('node:path');
const { pathToFileURL } = require('node:url');
const { writeFileSync, mkdirSync } = require('node:fs');
const assert = require('node:assert/strict');
const wait = ms => new Promise(resolve => setTimeout(resolve, ms));
app.on('window-all-closed', () => {});
app.setPath('userData', '/tmp/aiclaw-overlay-scale-' + process.pid);
let child, controller, indicator, calibration, overlay;
const watchdog = setTimeout(() => { console.error('X11 scale acceptance timed out'); finish(1); }, 90000);
const native = (kind, value) => JSON.parse(execFileSync('/usr/bin/python3', [join(__dirname,'overlay_geometry.py'), kind, ...(value === undefined ? [] : [String(value)])], {encoding:'utf8'}));
const xid = window => window.getNativeWindowHandle().readUInt32LE(0);
async function until(read, condition, description) {
 let lastError;
 for(let i=0;i<60;i++){try {const value=await read();if(condition(value))return value;}catch(error){lastError=String(error);}await wait(100);}
 throw Error('Timed out: ' + description + (lastError ? '; '+lastError : ''));
}
app.whenReady().then(async()=>{
 assert.equal(process.platform,'linux');
 const scale=Number(app.commandLine.getSwitchValue('force-device-scale-factor'))||1;
 const displays=()=>screen.getAllDisplays();
 assert.ok(displays().every(display=>Math.abs(display.scaleFactor-scale)<0.001));
 calibration=new BrowserWindow({x:80,y:110,width:400,height:240,show:true,frame:false,webPreferences:{sandbox:true,contextIsolation:true,nodeIntegration:false}});
 await calibration.loadURL('data:text/html,<body style="background:navy;color:white">Owned DIP calibration window</body>');
 await wait(250);
 const dip=calibration.getBounds(), physical=native('frame',xid(calibration));
 assert.ok(Math.abs(physical.width / dip.width-scale)<0.01);
 assert.ok(Math.abs(physical.height / dip.height-scale)<0.01);
 for(const key of ['x','y','width','height'])assert.ok(Math.abs(physical[key]/scale-dip[key])<=1, 'real X11 calibration differs from Electron DIP '+key);
 const {ComputerController}=await import(pathToFileURL('/aiclaw/dist/main/computer.js'));
 const {ComputerOverlay,overlayPlacement,overlayTargetFrame}=await import(pathToFileURL('/aiclaw/dist/main/computer-overlay.js'));
 controller=new ComputerController();
 child=spawn('/usr/bin/python3',[join(__dirname,'overlay_fixture.py')],{stdio:['ignore','pipe','inherit'],env:{...process.env,NO_AT_BRIDGE:'0',GTK_MODULES:'atk-bridge'}});
 await Promise.race([once(child.stdout,'data'),wait(10000).then(()=>{throw Error('GTK scale fixture startup timed out');})]);
 const target=String(child.pid), sessionId='x11-scale',turnId='scale';
 const result=await until(()=>controller.perform({sessionId,turnId,action:'query',arguments:{app:target,text:'Fixture input',exact:true}}),result=>JSON.parse(result.text.split('\n')[0]).matches.length===1,'native query fixture');
 const receipt=JSON.parse(result.text.split('\n')[0]), targetPixels=native('frame',receipt.window_id);
 assert.deepEqual(receipt.window_frame,targetPixels,'production query must expose actual X11 pixel bounds');
 const proposed=Object.fromEntries(Object.entries(targetPixels).map(([key,value])=>[key,Math.round(value/scale)]));
 let placement=overlayPlacement(proposed,displays());
 const createIndicator=()=>indicator=new BrowserWindow({...placement,show:false,frame:false,alwaysOnTop:true,resizable:false,webPreferences:{sandbox:true,contextIsolation:true,nodeIntegration:false}});
 if(process.env.AICLAW_OVERLAY_PROBE_ONLY==='1'){
  createIndicator(); await indicator.loadURL('data:text/html,<body style="background:green;color:white">Owned native alignment probe</body>');indicator.showInactive();
 } else {
  assert.deepEqual(overlayTargetFrame('linux',targetPixels,displays()),proposed,'production conversion must match measured X11-to-DIP mapping');
  overlay=new ComputerOverlay({platform:'linux',displays,readBounds:activity=>controller.windowBounds(activity),control:action=>controller.setControl(action),createWindow:createIndicator,loadWindow:window=>window.loadURL('data:text/html,<body style="background:green;color:white">Owned native alignment probe</body>'),sendStatus:()=>{}});
  overlay.update(controller.getStatus());
  await until(()=>indicator?.isVisible(),value=>value,'visible production overlay manager');
 }
 await wait(250);
 const indicatorPixels=native('frame',xid(indicator));
 const tolerance=Math.ceil(scale);
 assert.ok(Math.abs(indicatorPixels.x+indicatorPixels.width-targetPixels.x-targetPixels.width)<=tolerance,'actual X11 indicator right edge must align with GTK target');
 assert.ok(Math.abs(indicatorPixels.y+indicatorPixels.height+8*scale-targetPixels.y)<=tolerance,'actual X11 indicator must sit above GTK target with scaled gap');
 const indicatorDip=indicator.getBounds(), indicatorOuterPixels=native('outer',xid(indicator));
 for(const key of ['x','y','width','height'])assert.ok(Math.abs(indicatorDip[key]*scale-indicatorOuterPixels[key])<=tolerance, 'Electron indicator DIP getter must agree with measured X11 outer '+key);
 const monitorBefore=native('monitors'), monitorAfter=native('layout');await wait(600);
 const electronAfter=displays().map(({bounds,workArea,scaleFactor})=>({bounds,workArea,scaleFactor}));
 const summary={passed:true,platform:'linux-x11',architecture:process.arch,isolated_root_fixture_no_sandbox:app.commandLine.hasSwitch('no-sandbox'),requested_scale:scale,probe_only:process.env.AICLAW_OVERLAY_PROBE_ONLY==='1',electron_displays:electronAfter,calibration:{electron_dip:dip,x11_pixels:physical},native_query_matches_x11:true,target_pixels:targetPixels,converted_dip:proposed,indicator_pixels:indicatorPixels,indicator_dip:indicatorDip,indicator_outer_pixels:indicatorOuterPixels,physical_edge_alignment_verified:true,randr_before:monitorBefore,randr_after:monitorAfter,electron_multi_monitor_observed:electronAfter.length>1};
 if(process.env.AICLAW_COMPUTER_EVIDENCE_DIR){mkdirSync(process.env.AICLAW_COMPUTER_EVIDENCE_DIR,{recursive:true});writeFileSync(join(process.env.AICLAW_COMPUTER_EVIDENCE_DIR,`linux-overlay-scale-${scale}${summary.probe_only?'-probe':''}.json`),JSON.stringify(summary,null,2)+'\n');}
 console.log(JSON.stringify(summary));
}).then(()=>finish(0)).catch(error=>{console.error(error);finish(1);});
async function finish(code){clearTimeout(watchdog);overlay?.dispose();indicator?.destroy();calibration?.destroy();try{await controller?.close();}finally{child?.kill('SIGTERM');app.exit(code);}}
