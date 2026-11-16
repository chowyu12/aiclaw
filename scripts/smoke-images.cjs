/** Real Chromium decoding: bytes, transparency, scaling and EXIF orientation. */
const {app,BrowserWindow}=require('electron');
const {buildSync}=require('esbuild');
const {join}=require('node:path');
app.setPath('userData',join(app.getPath('temp'),'aiclaw-image-smoke'));
app.whenReady().then(async()=>{
 const win=new BrowserWindow({show:false,webPreferences:{contextIsolation:true,nodeIntegration:false}});
 await win.loadURL('about:blank');
 const bundle=buildSync({entryPoints:[join(__dirname,'../apps/desktop/src/renderer/attachments.ts')],bundle:true,write:false,format:'iife',globalName:'imageAttachments',platform:'browser'}).outputFiles[0].text;
 await win.webContents.executeJavaScript(bundle);
 const checks=await win.webContents.executeJavaScript(`(async()=>{
  const checks=[];const check=(name,ok)=>checks.push({name,ok});
  const {readImage,MAX_IMAGE_EDGE}=imageAttachments;
  const canvas=document.createElement('canvas');canvas.width=20;canvas.height=10;
  const ctx=canvas.getContext('2d');ctx.fillStyle='red';ctx.fillRect(0,0,5,5);
  const blob=async type=>new Promise(resolve=>canvas.toBlob(resolve,type));
  const file=async type=>new File([await blob(type)],type==='image/png'?'alpha.png':'photo.jpg',{type});
  const decode=async src=>{const image=new Image();image.src=src;await image.decode();return image;};
  const alpha=async src=>{const image=await decode(src),out=document.createElement('canvas');out.width=image.width;out.height=image.height;const c=out.getContext('2d');c.drawImage(image,0,0);return c.getImageData(image.width-1,image.height-1,1,1).data[3];};
  for(const type of ['image/png','image/jpeg']){
   const f=await file(type),result=await readImage(f);
   check(type+' preserves source bytes',atob(result.data)===String.fromCharCode(...new Uint8Array(await f.arrayBuffer())));
  }
  check('small PNG preserves transparency',await alpha((await readImage(await file('image/png'))).preview)===0);
  canvas.width=MAX_IMAGE_EDGE*2;canvas.height=MAX_IMAGE_EDGE;
  const large=await readImage(await file('image/png')),decoded=await decode(large.preview);
  check('large PNG resizes without losing alpha',decoded.width===MAX_IMAGE_EDGE&&decoded.height===MAX_IMAGE_EDGE/2&&await alpha(large.preview)===0);
  const oriented=async()=>{
   const raw=new Uint8Array(await (await blob('image/jpeg')).arrayBuffer());
   const exif=Uint8Array.from([255,225,0,34,69,120,105,102,0,0,73,73,42,0,8,0,0,0,1,0,18,1,3,0,1,0,0,0,6,0,0,0,0,0,0,0]);
   return new File([raw.subarray(0,2),exif,raw.subarray(2)],'rotated.jpg',{type:'image/jpeg'});
  };
  canvas.width=20;canvas.height=10;
  const small=await oriented(),smallResult=await readImage(small),smallImage=await decode(smallResult.preview);
  check('native JPEG retains EXIF orientation and bytes',smallImage.width===10&&smallImage.height===20&&atob(smallResult.data).length===small.size);
  canvas.width=MAX_IMAGE_EDGE*2;canvas.height=MAX_IMAGE_EDGE;
  const rotated=await readImage(await oriented()),rotatedImage=await decode(rotated.preview);
  check('resized JPEG applies EXIF orientation once',rotatedImage.width===MAX_IMAGE_EDGE/2&&rotatedImage.height===MAX_IMAGE_EDGE);
  return checks;
 })()`);
 for(const {name,ok} of checks)console.log(`${ok?'ok':'FAIL'} ${name}`);
 if(checks.some(check=>!check.ok))process.exitCode=1;
 win.destroy();app.quit();
}).catch(error=>{console.error(error);process.exitCode=1;app.quit();});
