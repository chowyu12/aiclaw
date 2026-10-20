import { randomUUID } from "node:crypto";
import type { AppConfigRequest, PendingAppConfigAction } from "@aiclaw/agent-client";
import type { ConfigStore, McpServer } from "./config.js";
import type { SessionManager } from "./session.js";
import type { Scheduler } from "./scheduler.js";
import type { ConfigurationCard, ConfigurationField, ConfigurationReply, ConfigurationOutcome } from "../shared/app-config.js";
import type { ScheduleRule } from "../shared/schedule.js";
import { tr } from "../shared/i18n.js";

type ConfigSessions = Pick<SessionManager, "listPlugins" | "pluginConfig" | "setPluginConfig" | "togglePlugin" | "deletePlugin" | "installPlugin" | "emailTest" | "listSearchEngines" | "createSearchEngine" | "updateSearchEngine" | "deleteSearchEngine" | "testSearchEngine" | "probeMcp" | "mcpOAuthLogin" | "mcpOAuthStatus">;
type ConfigStoreAccess = Pick<ConfigStore, "readMcpServers" | "writeMcpServers">;
const operations: Record<string, string[]> = {
  email: ["save", "enable", "disable", "test"],
  search: ["save", "default", "enable", "disable", "delete", "test"],
  mcp: ["save", "enable", "disable", "delete", "test", "login"],
  plugin: ["save", "install", "enable", "disable", "delete"],
  schedule: ["save", "enable", "disable", "delete"],
};
const publicFields: Record<string, string[]> = {
  email: ["address", "name", "username", "imap_host", "imap_port", "smtp_host", "smtp_port"],
  search: ["name", "provider", "baseUrl"], mcp: ["label", "transport", "url", "command", "args", "oauth"],
  plugin: ["connectionId"], schedule: ["name", "prompt", "kind", "time", "days", "everyMinutes", "at", "mode", "notificationPolicy", "stopWhen", "workspace"],
};
interface Pending {
  card: ConfigurationCard;
  action: PendingAppConfigAction;
  apply: (values: Record<string, string | boolean>) => Promise<string>;
  busy: boolean;
  expiresAt: number;
  timer: ReturnType<typeof setTimeout>;
}

/** The model proposes public fields; only this host-owned card can authorize a write. */
export class ConfigurationAssistant {
  private pending = new Map<string, Pending>();
  private generation = 0;
  private saving = false;
  private sessionGenerations = new Map<string, number>();
  private readonly sessions: ConfigSessions;
  private readonly store: ConfigStoreAccess;
  private readonly scheduler: Scheduler;
  private readonly changed: (cards: ConfigurationCard[]) => void;
  constructor(sessions: ConfigSessions, store: ConfigStoreAccess, scheduler: Scheduler, changed: (cards: ConfigurationCard[]) => void) {
    this.sessions = sessions; this.store = store; this.scheduler = scheduler; this.changed = changed;
  }

  listPending(): ConfigurationCard[] { return [...this.pending.values()].map(entry => entry.card); }
  clear(sessionId?: string): void {
    if (sessionId) this.sessionGenerations.set(sessionId, (this.sessionGenerations.get(sessionId) ?? 0) + 1);
    else this.generation++;
    for (const [id, entry] of this.pending) {
      if (!sessionId || entry.card.sessionId === sessionId) {
        clearTimeout(entry.timer);
        this.pending.delete(id);
      }
    }
    this.changed(this.listPending());
  }
  async request(action: PendingAppConfigAction): Promise<void> {
    const expiresAt = Date.now() + 29 * 60_000;
    const generation = this.generation, sessionGeneration = this.sessionGenerations.get(action.request.sessionId) ?? 0;
    try {
      const request = action.request;
      if (!Object.hasOwn(operations, request.category) || !["list", "configure"].includes(request.action)) throw Error("Unsupported configuration category or action");
      if (request.action === "list") { action.respond({text: JSON.stringify(await this.status(request.category))}); return; }
      if (!operations[request.category]!.includes(request.operation ?? "")) throw Error("Unsupported configuration operation");
      if (!request.values || typeof request.values === "object" && !Array.isArray(request.values)) {
        for (const key of Object.keys(request.values ?? {})) if (!publicFields[request.category]!.includes(key)) throw Error("Unsupported proposal field. Enter credentials and plugin settings directly in the configuration card.");
      } else throw Error("Invalid configuration proposal");
      const categoryNames = {email:tr("邮件"),search:tr("搜索引擎"),mcp:"MCP",plugin:tr("插件"),schedule:tr("定时任务")};
      const operationNames: Record<string,string> = {save:tr("保存配置"),default:tr("设为默认"),enable:tr("启用"),disable:tr("停用"),delete:tr("删除"),test:tr("测试连接"),login:tr("登录授权"),install:tr("安装插件")};
      const card: ConfigurationCard = {id: randomUUID(), sessionId: request.sessionId, category: request.category,
        operation: request.operation!, title: `${tr("应用配置")} · ${categoryNames[request.category]} · ${operationNames[request.operation!]}`, fields: [],
        note: tr("密钥只在此卡片填写，不会发送给模型。保存会修改应用配置；工具在当前任务结束后重新挂载生效。")};
      const apply = await this.prepare(request, card);
      if (generation !== this.generation || sessionGeneration !== (this.sessionGenerations.get(request.sessionId) ?? 0)) { return; }
      const timer = setTimeout(() => {
        if (!this.pending.has(card.id)) return;
        this.pending.delete(card.id); this.changed(this.listPending());
        action.respond({text:"Configuration request expired. Open a new card to continue."});
      }, Math.max(0, expiresAt - Date.now()));
      timer.unref();
      this.pending.set(card.id, {card, action, apply, busy:false, expiresAt, timer});
      this.changed(this.listPending());
    } catch {
      if (generation === this.generation && sessionGeneration === (this.sessionGenerations.get(action.request.sessionId) ?? 0)) {
        action.fail("Unable to prepare configuration. Check the category, operation and target ID; credentials must be entered in the desktop card.");
      }
    }
  }

  async respond(reply: ConfigurationReply): Promise<ConfigurationOutcome> {
    const entry = this.pending.get(reply.id);
    if (!entry || entry.busy || Date.now() >= entry.expiresAt) throw Error(tr("配置请求已结束或正在保存"));
    if (reply.cancel) {
      clearTimeout(entry.timer);
      this.pending.delete(reply.id); this.changed(this.listPending());
      entry.action.respond({text:"User cancelled configuration. No further changes will be applied. Any changes saved before a failed test are retained."});
      return {text:tr("已取消")};
    }
    if (this.saving) throw Error(tr("另一个配置正在保存，请稍后重试"));
    const values = reply.values;
    if (!values || typeof values !== "object" || Array.isArray(values)) throw Error("Invalid form values");
    for (const key of Object.keys(values)) if (!entry.card.fields.some(field => field.key === key)) throw Error("Unknown form field");
    for (const field of entry.card.fields) {
      const value = values[field.key];
      if (field.kind === "boolean" ? typeof value !== "boolean" : typeof value !== "string") throw Error(`Invalid field: ${field.label}`);
      if (field.required && !field.isSet && !String(value).trim()) throw Error(`${field.label}: required`);
      if (field.choices && !field.choices.includes(String(value))) throw Error(`Invalid choice: ${field.label}`);
    }
    entry.busy = true; this.saving = true;
    try {
      const text = await entry.apply(values);
      clearTimeout(entry.timer);
      this.pending.delete(reply.id); this.changed(this.listPending());
      // Only this fixed, non-secret summary is sent back to the model.
      entry.action.respond({text});
      return {text};
    } catch (error) {
      if (error instanceof SyntaxError) throw Error(tr("JSON 格式不正确，请检查引号、逗号和括号。"));
      // Provider errors can echo request headers or credentials. Keep them out of IPC and tool results.
      throw Error(tr("配置或测试未完成。部分设置可能已保存；请检查设置页后重试，或取消。详细凭据不会回传。"));
    } finally { entry.busy = false; this.saving = false; }
  }

  private async status(category: string): Promise<unknown> {
    if (category === "mcp") return Promise.all(this.store.readMcpServers().map(async s => ({id:s.id, name:s.label, transport:s.transport, enabled:s.enabled, credentialsSet:Boolean(Object.keys(s.env ?? {}).length || Object.keys(s.headers ?? {}).length), oauth:s.oauth === true, authState:s.oauth && s.url ? (await this.sessions.mcpOAuthStatus(s.url)).state : undefined})));
    if (category === "search") return (await this.sessions.listSearchEngines()).map(s => ({id:String(s.id), name:s.name, provider:s.provider, enabled:s.enabled, apiKeySet:s.apiKeySet}));
    if (category === "schedule") return this.scheduler.list().map(s => ({id:s.id, name:s.name, enabled:s.enabled, rule:s.rule, nextRunAt:s.nextRunAt, lastStatus:s.lastStatus}));
    return (await this.sessions.listPlugins()).filter(p => category !== "email" || p.pluginId === "aiclaw.email").map(p => ({id:p.uuid, name:p.name, pluginId:p.pluginId, enabled:p.enabled, source:p.source, missingConfig:p.missingConfig, permissions:p.permissions, connections:p.connections.map(c => ({id:c.uuid,name:c.name,missingConfig:c.missingConfig}))}));
  }

  private async prepare(r: AppConfigRequest, card: ConfigurationCard): Promise<Pending["apply"]> {
    const op = r.operation!, proposal = r.values ?? {};
    const field = (key:string, label:string, value:unknown = "", kind:ConfigurationField["kind"] = "text", choices?:string[], secret = false, isSet = false, required = false) => {
      const proposed = Object.hasOwn(proposal,key) ? proposal[key] : value;
      card.fields.push({key, label, kind, value: secret ? "" : kind === "boolean" ? proposed === true : typeof proposed === "object" ? JSON.stringify(proposed) : String(proposed ?? ""), choices, secret, isSet, required});
    };
    const assertPending = () => { if (!this.pending.has(card.id)) throw Error("Configuration expired"); };
    const complete = "Configuration saved. Tool changes apply after the current task finishes and tools are remounted; schedules take effect immediately.";
    if (r.category === "email" || r.category === "plugin") {
      const plugins = await this.sessions.listPlugins();
      let plugin = r.category === "email" ? plugins.find(p => p.pluginId === "aiclaw.email") : plugins.find(p => p.uuid === r.targetId);
      if (op === "install") {
        field("path",tr("插件目录"),"","text",undefined,false,false,true);
        card.note += " " + tr("安装将读取此目录的插件；启用后可能启动程序并获得插件声明的权限。");
        return async v => { await this.sessions.installPlugin(String(v.path)); return "Plugin installed. Review its permissions before enabling."; };
      }
      if (!plugin) throw Error("Unknown plugin");
      const connectionId = String(proposal.connectionId ?? (plugin.connections.length === 1 ? plugin.connections[0]!.uuid : ""));
      if (plugin.channels > 0 && op === "save" && !plugin.connections.some(c => c.uuid === connectionId)) throw Error("Select an existing connection ID from app_config_list; create new channel connections in Settings");
      if (connectionId && plugin.channels === 0) throw Error("This plugin has no channel connections");
      if (connectionId) card.note += ` Connection: ${plugin.connections.find(c => c.uuid === connectionId)?.name ?? connectionId}.`;
      const uuid = plugin.uuid;
      card.title += ` · ${plugin.name}`;
      card.note += ` Permissions: ${plugin.permissions.join(", ")}.`;
      if (op === "delete" && plugin.source === "builtin") throw Error("Built-in plugins cannot be removed");
      if (op === "save") {
        const fields = await this.sessions.pluginConfig(uuid, connectionId);
        for (const f of fields) field(f.key, f.description || f.key, f.value, f.secret ? "password" : "text", undefined, f.secret, f.isSet, f.required);
        for (const key of Object.keys(proposal)) if (key !== "connectionId" && !fields.some(f => f.key === key && !f.secret)) throw Error("Invalid plugin field");
      }
      return async v => {
        if (op === "delete") { await this.sessions.deletePlugin(uuid); return "Plugin removed."; }
        if (op === "disable") { await this.sessions.togglePlugin(uuid,false); return complete; }
        if (op === "save") {
          for (const f of card.fields) { assertPending(); if (!f.secret || v[f.key]) await this.sessions.setPluginConfig(uuid,f.key,String(v[f.key]),connectionId); }
        }
        assertPending();
        if (r.category === "email") {
          const test = await this.sessions.emailTest(uuid);
          if (!test.ok) return op === "test" ? "Email connection test failed. Configuration was not changed." : "Email settings saved, but connection test failed. Check the mail server and authorization code in Settings. Email was not newly enabled.";
          assertPending();
          if (op !== "test") await this.sessions.togglePlugin(uuid,true);
          return "Email connection test passed. " + (op === "test" ? "Configuration was not changed." : complete);
        }
        if (op === "enable") await this.sessions.togglePlugin(uuid,true);
        return complete + " Plugin configuration was validated; no general connection test is available.";
      };
    }
    if (r.category === "search") {
      const engines = await this.sessions.listSearchEngines();
      let engine = engines.find(s => String(s.id) === r.targetId);
      if (r.targetId && !engine || !engine && !["save","default"].includes(op)) throw Error("Unknown search engine");
      if (engine) card.title += ` · ${engine.name}`;
      if (["save","default"].includes(op)) {
        field("name",tr("名称"),engine?.name ?? "");
        field("provider",tr("搜索引擎"),engine?.provider ?? "tavily","select",["tavily","serpapi","aliyun-iqs"]);
        field("baseUrl",tr("服务地址"),engine?.baseUrl ?? "");
        field("apiKey","API Key","","password",undefined,true,engine?.apiKeySet,false);
      }
      return async v => {
        if (op === "delete") { await this.sessions.deleteSearchEngine(engine!.id); return "Search engine removed."; }
        if (["save","default"].includes(op)) {
          const patch = {name:String(v.name), provider:String(v.provider), baseUrl:String(v.baseUrl), ...(v.apiKey ? {apiKey:String(v.apiKey)} : {})};
          engine = engine ? await this.sessions.updateSearchEngine({id:engine.id,...patch}) : await this.sessions.createSearchEngine({...patch,enabled:false});
        }
        if (op === "disable") { await this.sessions.updateSearchEngine({id:engine!.id,enabled:false}); return complete; }
        if (!engine!.apiKeySet) return "No search API key is configured. Enter a key in Settings before enabling or testing. Any newly created engine remains disabled.";
        await this.sessions.testSearchEngine(engine!.id,"AIClaw");
        assertPending();
        if (op === "default") for (const other of await this.sessions.listSearchEngines()) { assertPending(); if (other.id !== engine!.id && other.enabled) await this.sessions.updateSearchEngine({id:other.id,enabled:false}); }
        if (["enable","default"].includes(op)) await this.sessions.updateSearchEngine({id:engine!.id,enabled:true});
        return "Search connection test passed. " + (op === "test" ? "Configuration was not changed." : complete);
      };
    }
    if (r.category === "mcp") {
      let server = this.store.readMcpServers().find(s => s.id === r.targetId);
      if (r.targetId && !server || !server && op !== "save") throw Error("Unknown MCP server");
      if (server) card.title += ` · ${server.label}`;
      let original = JSON.stringify(server);
      if (op === "save") {
        field("label",tr("名称"),server?.label ?? "");
        field("transport",tr("传输方式"),server?.transport ?? "http","select",["http","stdio"]);
        field("url",tr("服务地址"),server?.url ?? ""); field("command",tr("启动命令"),server?.command ?? "");
        field("args",tr("启动参数（JSON 数组）"),server?.args ?? [],"json");
        field("env",tr("环境变量（JSON 对象）"),"","json",undefined,true,Boolean(Object.keys(server?.env ?? {}).length));
        field("headers",tr("请求头（JSON 对象）"),"","json",undefined,true,Boolean(Object.keys(server?.headers ?? {}).length));
        field("oauth","OAuth",server?.oauth ?? false,"boolean");
        field("import",tr("导入 MCP JSON（可选）"),"","json",undefined,true);
        field("enabled",tr("启用"),server?.enabled ?? false,"boolean");
      }
      card.note += " " + tr("stdio 测试与启用会执行配置中的命令；请核对命令、参数和服务来源。");
      return async v => {
        const servers = this.store.readMcpServers();
        const commit = (next: McpServer | null) => {
          assertPending();
          const latest = this.store.readMcpServers();
          if (server && JSON.stringify(latest.find(s => s.id === server!.id)) !== original) throw Error("Configuration changed; reopen card");
          this.store.writeMcpServers(next ? server ? latest.map(s => s.id === server!.id ? next : s) : [...latest,next] : latest.filter(s => s.id !== server!.id));
        };
        if (server && JSON.stringify(servers.find(s => s.id === server!.id)) !== original) throw Error("Configuration changed; reopen card");
        if (op === "delete") { commit(null); return "MCP server removed."; }
        if (op === "login") {
          if (!server?.url || !server.oauth) throw Error("OAuth not configured");
          const result = await this.sessions.mcpOAuthLogin({url:server.url,clientId:server.oauthClientId,scope:server.oauthScope,redirectPort:server.oauthRedirectPort});
          return `OAuth authorization status: ${result.state}. Continue in the browser if authorization is pending.`;
        }
        let next: McpServer = {...server!, id:server?.id ?? `m_${randomUUID()}`, label:server?.label ?? "", transport:server?.transport ?? "http", enabled:server?.enabled ?? false};
        if (op === "save") {
          const object = (value:unknown): Record<string,string> => { const parsed = JSON.parse(String(value)); if (!parsed || Array.isArray(parsed) || typeof parsed !== "object" || Object.values(parsed).some(x => typeof x !== "string")) throw Error("Expected string map"); return parsed; };
          const args = JSON.parse(String(v.args)); if (!Array.isArray(args) || args.some(x => typeof x !== "string")) throw Error("Expected argument array");
          next = {...next,label:String(v.label),transport:v.transport as "http"|"stdio",url:String(v.url),command:String(v.command),args,oauth:v.oauth === true,enabled:v.enabled === true};
          if (v.env) next.env = object(v.env); if (v.headers) next.headers = object(v.headers);
          if (v.import) {
            let imported = JSON.parse(String(v.import));
            if (imported.mcpServers) { const entries = Object.entries(imported.mcpServers); if (entries.length !== 1) throw Error("Import one server per card"); next.label = entries[0]![0]; imported = entries[0]![1]; }
            if (!imported || typeof imported !== "object" || Array.isArray(imported)) throw Error("Invalid MCP import");
            for (const key of ["command","url"]) if (imported[key] !== undefined && typeof imported[key] !== "string") throw Error("Invalid MCP import");
            if (imported.args !== undefined && (!Array.isArray(imported.args) || imported.args.some((x:unknown) => typeof x !== "string"))) throw Error("Invalid MCP arguments");
            next = {...next,transport:imported.url ? "http":"stdio",url:imported.url,command:imported.command,args:imported.args ?? [],...(imported.env ? {env:object(JSON.stringify(imported.env))}:{}),...(imported.headers ? {headers:object(JSON.stringify(imported.headers))}:{})};
          }
        }
        if (op === "enable") next.enabled=true; if (op === "disable") next.enabled=false;
        if (op !== "disable" && next.transport === "http") { const url = new URL(next.url ?? ""); if (!["https:","http:"].includes(url.protocol)) throw Error("Invalid MCP URL"); }
        else if (op !== "disable" && next.transport === "stdio" && !next.command?.trim()) throw Error("Missing MCP command");
        if (op !== "disable") {
          const probe = await this.sessions.probeMcp(next);
          assertPending();
          if (!probe.ok) {
            if (op === "save" && next.oauth) {
              next.enabled = false;
              commit(next);
              server = next; original = JSON.stringify(next);
              return "MCP saved disabled. Connection was not verified; complete OAuth login before enabling it.";
            }
            return "MCP connection test failed. No MCP configuration was saved. Check the command, endpoint and credentials in the card.";
          }
        }
        if (op !== "test") { assertPending(); commit(next); server = next; original = JSON.stringify(next); }
        return (op === "disable" ? "MCP disabled. " : "MCP connection test passed. ") + (op === "test" ? "Configuration was not changed." : complete);
      };
    }
    let task = this.scheduler.list().find(s => s.id === r.targetId);
    if (r.targetId && !task || !task && op !== "save") throw Error("Unknown scheduled task");
    if (task) card.title += ` · ${task.name}`;
    if (op === "save") {
      field("name",tr("名称"),task?.name ?? "","text",undefined,false,false,true);
      field("prompt",tr("任务内容"),task?.prompt ?? "","text",undefined,false,false,true);
      field("kind",tr("运行频率"),task?.rule.kind ?? "daily","select",["daily","weekdays","weekly","interval","once"]);
      field("time",tr("本地时间"),task?.rule.time ?? "09:00"); field("days",tr("星期（JSON 数组，0 为周日）"),task?.rule.days ?? [],"json");
      field("everyMinutes",tr("间隔分钟"),task?.rule.everyMinutes ?? 60); field("at",tr("单次执行时间（ISO 8601）"),task?.rule.at ?? "");
      field("mode",tr("任务模式"),task?.mode ?? "followup","select",["followup","cron"]);
      field("notificationPolicy",tr("通知策略"),task?.notificationPolicy ?? "changes","select",["changes","all"]);
      field("stopWhen",tr("完成条件"),task?.stopWhen ?? ""); field("workspace",tr("工作区"),task?.workspace ?? "");
    }
    card.note += " " + tr("定时任务仅在应用运行时执行。保存后可在设置中查看下一次运行时间。");
    return async v => {
      if (task && !this.scheduler.list().some(s => s.id === task!.id)) throw Error("Task no longer exists");
      if (op === "delete") { if (!this.scheduler.remove(task!.id)) throw Error("Task not found"); return "Scheduled task removed."; }
      if (op !== "save") { this.scheduler.setEnabled(task!.id,op === "enable"); return complete; }
      const rule: ScheduleRule = {kind:v.kind as ScheduleRule["kind"], time:String(v.time), days:JSON.parse(String(v.days)), everyMinutes:Number(v.everyMinutes),at:String(v.at)};
      task = this.scheduler.save({id:task?.id,name:String(v.name),prompt:String(v.prompt),rule,workspace:String(v.workspace),mode:v.mode as "followup"|"cron",sessionId:task?.sessionId || r.sessionId,notificationPolicy:v.notificationPolicy as "changes"|"all",stopWhen:String(v.stopWhen)});
      return `Scheduled task saved. Next run: ${task.nextRunAt ?? "none"}. Runs only while the app is open.`;
    };
  }
}
