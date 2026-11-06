/** Independent Electron input fixture, with its own bundle identifier. */
const { app, BrowserWindow } = require("electron");
const { readFile, writeFile, rename } = require("node:fs/promises");
const receipt = process.argv[2], commands = process.argv[3];
const fieldKind = process.argv[4] || "input";
app.setName("AIClaw Background Keyboard Electron Fixture");
app.setPath("userData", `${receipt}-profile`);
let window, secondWindow, previousCommand, previousReceipt, busy = false;
const field = fieldKind === "input" ? '<input id="editor" type="text" aria-label="Background keyboard target editor" value="seed" style="width:400px;height:40px">' : `<textarea id="editor" aria-label="Background keyboard target editor" style="width:400px;height:90px">${fieldKind === "textarea-empty" ? "" : "seed"}</textarea>`;
const page = `<!doctype html><meta charset="utf-8"><title>Independent Electron keyboard fixture</title>
<body style="font:16px sans-serif;padding:20px;background:white"><p>Public PID input fixture</p>
${field}
<input id="other" value="Other field"><input id="secure" type="password" value="fixture-only-password" style="display:none">
<script>
window.receivedDown=[];window.receivedUp=[];window.actualKeysDown=[];window.actualKeysUp=[];
window.inputEvents=[];window.businessValue=document.getElementById('editor').value;
const code=e=>e.key==='ArrowLeft'?123:e.key==='ArrowRight'?124:e.keyCode;
addEventListener('keydown',e=>{receivedDown.push(code(e));actualKeysDown.push([e.key,e.code]);receivedDown=receivedDown.slice(-256);actualKeysDown=actualKeysDown.slice(-256)});
addEventListener('keyup',e=>{receivedUp.push(code(e));actualKeysUp.push([e.key,e.code]);receivedUp=receivedUp.slice(-256);actualKeysUp=actualKeysUp.slice(-256)});
addEventListener('input',e=>{inputEvents.push({id:e.target.id,value:e.target.value,trusted:e.isTrusted});inputEvents=inputEvents.slice(-32);if(e.target.id==='editor')businessValue=e.target.value});
editor.focus();editor.setSelectionRange(editor.value.length,editor.value.length);
</script>`;
app.whenReady().then(async () => {
  app.setAccessibilitySupportEnabled(true);
  window = new BrowserWindow({width: 480, height: 300, x: 200, y: 250, show: false, webPreferences: {sandbox: true, contextIsolation: true, nodeIntegration: false}});
  await window.loadURL(`data:text/html;charset=utf-8,${encodeURIComponent(page)}`);
  window.show(); window.focus(); app.focus({steal: true});
  window.webContents.focus();
  await window.webContents.executeJavaScript(`const field=document.getElementById('editor');field.focus();field.setSelectionRange(${fieldKind === "input" ? "1,3" : "field.value.length,field.value.length"})`);
  setInterval(() => { if (!busy) { busy = true; tick().catch(error => console.error(error.message)).finally(() => { busy = false; }); } }, 50);
});
async function tick() {
  let data;
  try { data = await readFile(commands, "utf8"); } catch (error) { if (error.code !== "ENOENT") throw error; }
  if (data && data !== previousCommand) {
    previousCommand = data;
    switch (JSON.parse(data).command) {
      case "change-value": await window.webContents.executeJavaScript("document.getElementById('editor').value+='manual'"); break;
      case "change-caret": await window.webContents.executeJavaScript("document.getElementById('editor').setSelectionRange(0,0)"); break;
      case "reset-editor": await window.webContents.executeJavaScript("(()=>{const f=document.getElementById('editor');f.value='target-value';f.focus();f.setSelectionRange(5,5)})()"); break;
      case "focus-other": await window.webContents.executeJavaScript("document.getElementById('other').focus()"); break;
      case "focus-editor": await window.webContents.executeJavaScript("document.getElementById('secure').style.display='none';document.getElementById('editor').style.display='block';document.getElementById('editor').focus()"); break;
      case "secure": await window.webContents.executeJavaScript("document.getElementById('editor').style.display='none';document.getElementById('secure').style.display='block';document.getElementById('secure').focus()"); break;
      case "move": { const [x,y] = window.getPosition(); window.setPosition(x+20,y); break; }
      case "second-window":
        secondWindow = new BrowserWindow({width: 320, height: 180, show: false, webPreferences: {sandbox: true, contextIsolation: true, nodeIntegration: false}});
        await secondWindow.loadURL("data:text/html,<textarea autofocus>second-window</textarea>");
        secondWindow.showInactive(); secondWindow.webContents.focus(); break;
      case "activate": window.focus(); app.focus({steal: true}); break;
      case "close": window.close(); break;
    }
  }
  if (window.isDestroyed()) return;
  const state = await window.webContents.executeJavaScript(`(()=>{const editor=document.getElementById('editor');return {text:editor.value,other_text:document.getElementById('other').value,business_value:businessValue,input_events:inputEvents,selection:[editor.selectionStart,editor.selectionEnd-editor.selectionStart],editor_is_first_responder:document.activeElement===editor,events_down:receivedDown,events_up:receivedUp,actual_keys_down:actualKeysDown,actual_keys_up:actualKeysUp,secure_unchanged:document.getElementById('secure').value==='fixture-only-password'}})()`);
  const result = JSON.stringify({...state,role:"target",pid:process.pid,window_id:Number(window.getMediaSourceId().split(":")[1]),is_active:window.isFocused(),is_visible:window.isVisible()});
  if (result !== previousReceipt) { previousReceipt = result; await writeFile(`${receipt}.tmp`, result); await rename(`${receipt}.tmp`, receipt); }
}
app.on("window-all-closed", () => app.quit());
