/** Permission-free embedded Cua handshake. Never opens permission UI or sends input. */
const {app}=require("electron");
const {readFileSync,existsSync}=require("node:fs");
const path=require("node:path");
const {pathToFileURL}=require("node:url");
const watchdog=setTimeout(()=>{console.error("Embedded Cua smoke timed out");app.exit(1);},45_000);
app.whenReady().then(async()=>{
 const {CuaRuntime}=await import(pathToFileURL(path.join(__dirname,"../apps/desktop/dist/main/cua-runtime.js")));
 const runtime=await CuaRuntime.create();
 try {
  const specs=JSON.parse(readFileSync(path.join(__dirname,"../tools/claw-agent/internal/agent/computer-tools.json"),"utf8"));
  let nativeCount=0;
  for(const spec of specs){
   if(spec.action==="wait_new_window")continue;
   nativeCount++;
   const native=runtime.tools.find(tool=>tool.name===spec.action);if(!native)throw Error(`Missing Cua tool ${spec.action}`);
   for(const field of Object.keys(spec.schema.properties)){
    // The host adapts or rejects these fields before dispatch to Linux.
    if(process.platform === "linux" && (
      (spec.action === "get_window_state" && ["screenshot_format","screenshot_quality"].includes(field)) ||
      (spec.action === "click" && field === "action") ||
      (spec.action === "launch_app" && field === "creates_new_application_instance") ||
      field === "post_action_wait")) continue;
    if(["screenshot_format","screenshot_quality"].includes(field) && spec.action === "get_window_state" && process.platform === "win32")continue;
    if(!native.inputSchema.properties[field])throw Error(`Missing ${spec.action}.${field}`);
   }
  }
  const result=await runtime.call("check_permissions",{prompt:false});
  const permissions=result.structuredContent;
  if(process.platform==="darwin"&&(permissions?.source?.attribution!=="host"||permissions.source.embedded!==true))throw Error("Permissions must belong to the host");
  if(existsSync(path.join(__dirname,"../apps/desktop/cua-driver/CuaDriver.app")))throw Error("An independent app must not be bundled");
  console.log(`Embedded Cua MCP and ${nativeCount} driver contracts verified. Permission owner: host; no screenshots or input.`);
 }finally{await runtime.close();}
}).then(()=>{clearTimeout(watchdog);app.quit();}).catch(error=>{console.error(error);clearTimeout(watchdog);app.exit(1);});
