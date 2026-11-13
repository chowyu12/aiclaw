/** Interactive, background-only test against a new TextEdit document. */
import {mkdtemp,writeFile,rm} from "node:fs/promises";
import {join} from "node:path";
import {pathToFileURL} from "node:url";
import {execFileSync} from "node:child_process";
export async function textEditChecks(evaluate) {
  const temporary=await mkdtemp('/tmp/aiclaw-textedit-');
  const fixture=join(temporary,'aiclaw-cua-source.txt');
  await writeFile(fixture,'AIClaw disposable fixture');
  const existing=new Set(execFileSync('/bin/ps',['-axo','pid=,comm='],{encoding:'utf8'}).split('\n').filter(line=>line.includes('/System/Applications/TextEdit.app/Contents/MacOS/TextEdit')).map(line=>Number(line.trim().split(/\s+/)[0])));
  let result;
  try { result=await evaluate(`(async()=>{
    const require=process.getBuiltinModule('module').createRequire(process.resourcesPath+'/app/package.json');
    const {ComputerController}=require(process.resourcesPath+'/app/dist/main/computer.js');
    const controller=new ComputerController();
    const call=(action,args)=>controller.perform({sessionId:'textedit-fixture',turnId:'one',action,arguments:args});
    const report={};
    try {
      const launched=JSON.parse((await call('launch_app',{bundle_id:'com.apple.TextEdit',creates_new_application_instance:true,urls:[${JSON.stringify(pathToFileURL(fixture).href)}]})).text).data;
      const pid=launched.pid;report.fixture_pid=pid;
      if(!pid)throw Error('No TextEdit process returned: '+JSON.stringify(launched));
      if(${JSON.stringify([...existing])}.includes(pid)) throw Error('The system reused an existing TextEdit process; stopped to protect its documents');
      let found={windows:[]};
      for(let attempt=0;attempt<20&&!found.windows.some(w=>w.title==='aiclaw-cua-source.txt');attempt++) {
        found=JSON.parse((await call('list_windows',{pid})).text).data;
        found.windows=found.windows.filter(w=>w.layer===0&&w.title);
        if(!found.windows.some(w=>w.title==='aiclaw-cua-source.txt'))await new Promise(resolve=>setTimeout(resolve,250));
      }
      const initial=found.windows.find(w=>w.title==='aiclaw-cua-source.txt');
      if(!initial)throw Error('The isolated TextEdit fixture window did not appear');
      await call('invoke_menu',{pid,window_id:initial.window_id,path:['文件','新建']});
      const created=JSON.parse((await call('wait_new_window',{pid,exclude_window_ids:found.windows.map(w=>w.window_id),timeout_ms:5000})).text).data;
      if(created.windows.length!==1) throw Error('New document window not identified: '+JSON.stringify(created));
      const window_id=created.windows[0].window_id;report.new_window=true;
      const state=JSON.parse((await call('get_window_state',{pid,window_id,query:'AXTextArea',tree_format:'elements',include_screenshot:false,timeout_ms:4000})).text).data;
      const input=state.elements?.find(e=>e.role==='AXTextArea');
      if(!input?.element_token)throw Error('New document text area not found: '+JSON.stringify(state));
      await call('type_text',{pid,window_id,element_token:input.element_token,text:'AIClaw 后台保存验收\\n第一项：已完成\\n第二项：已完成 ✅'});report.input=true;
      const current=JSON.parse((await call('get_window_state',{pid,window_id,query:'AXTextArea',tree_format:'elements',include_screenshot:false,timeout_ms:4000})).text).data;
      if(!JSON.stringify(current).includes('第二项：已完成'))throw Error('Typed text could not be verified');report.input_verified=true;
      try {
        const save=await call('invoke_menu',{pid,window_id,path:['文件','保存']});report.save_command=save.text;
        await new Promise(resolve=>setTimeout(resolve,700));
        const windows=JSON.parse((await call('list_windows',{pid})).text).data;
        report.after_save_windows=windows.windows.map(w=>({window_id:w.window_id,title:w.title,layer:w.layer}));
        const after=JSON.parse((await call('get_window_state',{pid,window_id,include_screenshot:false,tree_format:'elements',timeout_ms:4000})).text).data;
        report.save_panel=JSON.stringify(after).includes('AXSheet');const sheet=after.elements?.find(e=>e.role==='AXSheet');report.save_fields=after.elements?.filter(e=>e.role==='AXSheet'||sheet&&e.parent_index===sheet.element_index).map(e=>({role:e.role,label:e.label,title:e.title,element_token:e.element_token,parent_index:e.parent_index,depth:e.depth}));
      } catch(error) {
        report.save_error=String(error);
        try {
          await call('invoke_menu',{pid,window_id,path:['文件','保存为…']});
          report.save_as_command=true;
          const after=JSON.parse((await call('get_window_state',{pid,window_id,include_screenshot:false,tree_format:'elements',timeout_ms:4000})).text).data;
          report.save_panel=JSON.stringify(after).includes('AXSheet');
          report.save_observation=JSON.stringify(after).slice(0,5000);
        } catch(fallback) { report.save_as_error=String(fallback); }
      }
      return report;
    } catch(error) {return {...report,error:String(error)};} finally {await controller.close();}
  })()`); return result; } finally {
    if(result?.fixture_pid && !existing.has(result.fixture_pid)) {
      try { const executable=execFileSync('/bin/ps',['-p',String(result.fixture_pid),'-o','comm='],{encoding:'utf8'}).trim();
        if(executable==='/System/Applications/TextEdit.app/Contents/MacOS/TextEdit') process.kill(result.fixture_pid,'SIGTERM');
      } catch {}
    }
    await rm(temporary,{recursive:true,force:true});
  }
}
