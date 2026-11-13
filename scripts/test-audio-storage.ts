import test from 'node:test';
import assert from 'node:assert/strict';
import { mkdtempSync,readFileSync,writeFileSync,rmSync,statSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join,relative } from 'node:path';
import { stageAudio } from '../apps/desktop/src/main/media-files.ts';
test('audio survives source-temp cleanup in managed storage without filename collisions',()=>{
 const root=mkdtempSync(join(tmpdir(),'aiclaw-audio-storage-')),source=mkdtempSync(join(tmpdir(),'aiclaw-audio-source-'));
 try {
  const file=join(source,'clip.wav');writeFileSync(file,Buffer.from('fixture-audio'));
  const data=readFileSync(file).toString('base64'),attachments=join(root,'attachments','audio');
  const stored=stageAudio('clip.wav',data,attachments),second=stageAudio('clip.wav',data,attachments);
  rmSync(source,{recursive:true,force:true});
  assert.ok(!relative(attachments,stored).startsWith('..'));assert.notEqual(stored,second);
  assert.equal(readFileSync(stored,'utf8'),'fixture-audio');
  if(process.platform!=='win32')assert.equal(statSync(stored).mode&0o777,0o600);
 } finally {rmSync(root,{recursive:true,force:true});rmSync(source,{recursive:true,force:true});}
});
