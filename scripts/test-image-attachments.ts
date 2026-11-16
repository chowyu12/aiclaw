import test from 'node:test';
import assert from 'node:assert/strict';
import {readImage,imageDataURL,MAX_IMAGE_EDGE} from '../apps/desktop/src/renderer/attachments.ts';

test('small PNG and JPEG keep original bytes; other or oversized images normalize once',async()=>{
 const oldBitmap=globalThis.createImageBitmap,oldDocument=globalThis.document;
 let width=20,height=10,closed=0;const formats:string[]=[];const orientations:unknown[]=[];
 (globalThis as any).createImageBitmap=async(_file:unknown,options:unknown)=>{orientations.push(options);return {width,height,close(){closed++}}};
 (globalThis as any).document={createElement:()=>({getContext:()=>({drawImage(){}}),toDataURL:(mime:string)=>{formats.push(mime);return `data:${mime};base64,YWJj`}})};
 try {
  for(const type of ['image/png','image/jpeg']){
   const file=new File([Uint8Array.from([1,2,3])],'fixture',{type});
   const result=await readImage(file);assert.equal(result.data,'AQID');assert.equal(result.preview,`data:${type};base64,AQID`);
  }
  assert.deepEqual(formats,[]);
  width=MAX_IMAGE_EDGE*2;height=MAX_IMAGE_EDGE;
  await readImage(new File(['x'],'alpha.png',{type:'image/png'}));assert.equal(formats.at(-1),'image/png');
  await readImage(new File(['x'],'photo.jpg',{type:'image/jpeg'}));assert.equal(formats.at(-1),'image/jpeg');
  width=20;height=10;await readImage(new File(['x'],'other.webp',{type:'image/webp'}));assert.equal(formats.at(-1),'image/jpeg');
  assert.equal(closed,5);assert.ok(orientations.every((option:any)=>option.imageOrientation==='from-image'));
  assert.match(imageDataURL('iVBORw0KGgo='),/^data:image\/png/);
 } finally {globalThis.createImageBitmap=oldBitmap;(globalThis as any).document=oldDocument;}
});
