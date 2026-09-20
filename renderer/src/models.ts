export namespace appservice {
	
	export class BackgroundChat {
	    request_id: string;
	    thread_id: string;
	    started_at: string;
	
	    static createFrom(source: any = {}) {
	        return new BackgroundChat(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.request_id = source["request_id"];
	        this.thread_id = source["thread_id"];
	        this.started_at = source["started_at"];
	    }
	}
	export class ChatProfile {
	    ProviderID: number;
	    ModelName: string;
	    SearchEnabled: boolean;
	    ProjectUUID: string;
	    RequestID: string;
	    MemoryUseEnabled: boolean;
	    MemoryGenerateEnabled: boolean;
	
	    static createFrom(source: any = {}) {
	        return new ChatProfile(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.ProviderID = source["ProviderID"];
	        this.ModelName = source["ModelName"];
	        this.SearchEnabled = source["SearchEnabled"];
	        this.ProjectUUID = source["ProjectUUID"];
	        this.RequestID = source["RequestID"];
	        this.MemoryUseEnabled = source["MemoryUseEnabled"];
	        this.MemoryGenerateEnabled = source["MemoryGenerateEnabled"];
	    }
	}
	export class ChatResult {
	    threadId: string;
	    content: string;
	    error?: string;
	
	    static createFrom(source: any = {}) {
	        return new ChatResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.threadId = source["threadId"];
	        this.content = source["content"];
	        this.error = source["error"];
	    }
	}
	export class ChatStartResult {
	    request_id: string;
	    thread_id: string;
	
	    static createFrom(source: any = {}) {
	        return new ChatStartResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.request_id = source["request_id"];
	        this.thread_id = source["thread_id"];
	    }
	}
	export class DesktopAttachment {
	    uuid: string;
	    filename: string;
	    content_type: string;
	    file_size: number;
	    file_type: string;
	    preview_url?: string;
	    available: boolean;
	
	    static createFrom(source: any = {}) {
	        return new DesktopAttachment(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.uuid = source["uuid"];
	        this.filename = source["filename"];
	        this.content_type = source["content_type"];
	        this.file_size = source["file_size"];
	        this.file_type = source["file_type"];
	        this.preview_url = source["preview_url"];
	        this.available = source["available"];
	    }
	}
	export class DesktopChannelBinding {
	    plugin_uuid: string;
	    channel_id: string;
	    external_key: string;
	    display_name: string;
	    thread_uuid: string;
	    provider_id: number;
	    model_name: string;
	    allowed: boolean;
	    allowed_tools: string[];
	    last_message: string;
	
	    static createFrom(source: any = {}) {
	        return new DesktopChannelBinding(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.plugin_uuid = source["plugin_uuid"];
	        this.channel_id = source["channel_id"];
	        this.external_key = source["external_key"];
	        this.display_name = source["display_name"];
	        this.thread_uuid = source["thread_uuid"];
	        this.provider_id = source["provider_id"];
	        this.model_name = source["model_name"];
	        this.allowed = source["allowed"];
	        this.allowed_tools = source["allowed_tools"];
	        this.last_message = source["last_message"];
	    }
	}
	export class DesktopExecution {
	    id: string;
	    name: string;
	    status: string;
	    message: string;
	    input?: string;
	    output?: string;
	    error?: string;
	    duration_ms?: number;
	
	    static createFrom(source: any = {}) {
	        return new DesktopExecution(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.status = source["status"];
	        this.message = source["message"];
	        this.input = source["input"];
	        this.output = source["output"];
	        this.error = source["error"];
	        this.duration_ms = source["duration_ms"];
	    }
	}
	export class DesktopMCPServer {
	    uuid: string;
	    name: string;
	    description: string;
	    transport: string;
	    endpoint: string;
	    enabled: boolean;
	    plugin_uuid: string;
	
	    static createFrom(source: any = {}) {
	        return new DesktopMCPServer(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.uuid = source["uuid"];
	        this.name = source["name"];
	        this.description = source["description"];
	        this.transport = source["transport"];
	        this.endpoint = source["endpoint"];
	        this.enabled = source["enabled"];
	        this.plugin_uuid = source["plugin_uuid"];
	    }
	}
	export class DesktopMemory {
	    uuid: string;
	    kind: string;
	    memory_key: string;
	    content: string;
	    status: string;
	    importance: number;
	    confidence: number;
	    sensitivity: string;
	    pinned: boolean;
	    updated_at: string;
	
	    static createFrom(source: any = {}) {
	        return new DesktopMemory(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.uuid = source["uuid"];
	        this.kind = source["kind"];
	        this.memory_key = source["memory_key"];
	        this.content = source["content"];
	        this.status = source["status"];
	        this.importance = source["importance"];
	        this.confidence = source["confidence"];
	        this.sensitivity = source["sensitivity"];
	        this.pinned = source["pinned"];
	        this.updated_at = source["updated_at"];
	    }
	}
	export class DesktopMemorySettings {
	    use_memories: boolean;
	    generate_memories: boolean;
	
	    static createFrom(source: any = {}) {
	        return new DesktopMemorySettings(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.use_memories = source["use_memories"];
	        this.generate_memories = source["generate_memories"];
	    }
	}
	export class DesktopOutputFile {
	    path: string;
	    filename: string;
	    content_type: string;
	    file_size: number;
	    file_type: string;
	    preview_url?: string;
	    description?: string;
	    available: boolean;
	
	    static createFrom(source: any = {}) {
	        return new DesktopOutputFile(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.path = source["path"];
	        this.filename = source["filename"];
	        this.content_type = source["content_type"];
	        this.file_size = source["file_size"];
	        this.file_type = source["file_type"];
	        this.preview_url = source["preview_url"];
	        this.description = source["description"];
	        this.available = source["available"];
	    }
	}
	export class DesktopMessage {
	    role: string;
	    content: string;
	    attachments?: DesktopAttachment[];
	    files?: DesktopOutputFile[];
	    execution?: DesktopExecution[];
	
	    static createFrom(source: any = {}) {
	        return new DesktopMessage(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.role = source["role"];
	        this.content = source["content"];
	        this.attachments = this.convertValues(source["attachments"], DesktopAttachment);
	        this.files = this.convertValues(source["files"], DesktopOutputFile);
	        this.execution = this.convertValues(source["execution"], DesktopExecution);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class DesktopModelSelection {
	    provider_id: number;
	    model_name: string;
	
	    static createFrom(source: any = {}) {
	        return new DesktopModelSelection(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.provider_id = source["provider_id"];
	        this.model_name = source["model_name"];
	    }
	}
	
	export class DesktopPlugin {
	    uuid: string;
	    name: string;
	    description: string;
	    version: string;
	    source: string;
	    enabled: boolean;
	    skill_count: number;
	    mcp_count: number;
	    tool_count: number;
	    channel_count: number;
	    permissions: string[];
	    missing_config: string[];
	
	    static createFrom(source: any = {}) {
	        return new DesktopPlugin(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.uuid = source["uuid"];
	        this.name = source["name"];
	        this.description = source["description"];
	        this.version = source["version"];
	        this.source = source["source"];
	        this.enabled = source["enabled"];
	        this.skill_count = source["skill_count"];
	        this.mcp_count = source["mcp_count"];
	        this.tool_count = source["tool_count"];
	        this.channel_count = source["channel_count"];
	        this.permissions = source["permissions"];
	        this.missing_config = source["missing_config"];
	    }
	}
	export class DesktopProject {
	    uuid: string;
	    name: string;
	
	    static createFrom(source: any = {}) {
	        return new DesktopProject(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.uuid = source["uuid"];
	        this.name = source["name"];
	    }
	}
	export class DesktopProvider {
	    id: number;
	    name: string;
	    type: string;
	    base_url: string;
	    models: string[];
	
	    static createFrom(source: any = {}) {
	        return new DesktopProvider(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.type = source["type"];
	        this.base_url = source["base_url"];
	        this.models = source["models"];
	    }
	}
	export class DesktopSearchEngine {
	    id: number;
	    provider: string;
	    name: string;
	    base_url: string;
	    enabled: boolean;
	    api_key_set: boolean;
	
	    static createFrom(source: any = {}) {
	        return new DesktopSearchEngine(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.provider = source["provider"];
	        this.name = source["name"];
	        this.base_url = source["base_url"];
	        this.enabled = source["enabled"];
	        this.api_key_set = source["api_key_set"];
	    }
	}
	export class DesktopThread {
	    uuid: string;
	    title: string;
	    project_uuid: string;
	    provider_id: number;
	    model_name: string;
	    updated_at: string;
	
	    static createFrom(source: any = {}) {
	        return new DesktopThread(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.uuid = source["uuid"];
	        this.title = source["title"];
	        this.project_uuid = source["project_uuid"];
	        this.provider_id = source["provider_id"];
	        this.model_name = source["model_name"];
	        this.updated_at = source["updated_at"];
	    }
	}
	export class MCPServerInput {
	    Name: string;
	    Description: string;
	    Transport: string;
	    Endpoint: string;
	    Args: string;
	    Env: string;
	    Headers: string;
	
	    static createFrom(source: any = {}) {
	        return new MCPServerInput(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.Name = source["Name"];
	        this.Description = source["Description"];
	        this.Transport = source["Transport"];
	        this.Endpoint = source["Endpoint"];
	        this.Args = source["Args"];
	        this.Env = source["Env"];
	        this.Headers = source["Headers"];
	    }
	}
	export class ProviderInput {
	    Name: string;
	    Type: string;
	    BaseURL: string;
	    APIKey: string;
	    Model: string;
	
	    static createFrom(source: any = {}) {
	        return new ProviderInput(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.Name = source["Name"];
	        this.Type = source["Type"];
	        this.BaseURL = source["BaseURL"];
	        this.APIKey = source["APIKey"];
	        this.Model = source["Model"];
	    }
	}
	export class SearchEngineInput {
	    Provider: string;
	    Name: string;
	    BaseURL: string;
	    APIKey: string;
	
	    static createFrom(source: any = {}) {
	        return new SearchEngineInput(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.Provider = source["Provider"];
	        this.Name = source["Name"];
	        this.BaseURL = source["BaseURL"];
	        this.APIKey = source["APIKey"];
	    }
	}
	export class WeChatLoginQR {
	    token: string;
	    image: string;
	
	    static createFrom(source: any = {}) {
	        return new WeChatLoginQR(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.token = source["token"];
	        this.image = source["image"];
	    }
	}
	export class WeChatLoginStatus {
	    status: string;
	    saved: boolean;
	
	    static createFrom(source: any = {}) {
	        return new WeChatLoginStatus(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.status = source["status"];
	        this.saved = source["saved"];
	    }
	}

}

export namespace plugin {
	
	export class ChannelStatus {
	    plugin_uuid: string;
	    plugin_name: string;
	    channel_id: string;
	    display_name: string;
	    state: string;
	    attempts: number;
	    last_error: string;
	    // Go type: time
	    started_at: any;
	
	    static createFrom(source: any = {}) {
	        return new ChannelStatus(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.plugin_uuid = source["plugin_uuid"];
	        this.plugin_name = source["plugin_name"];
	        this.channel_id = source["channel_id"];
	        this.display_name = source["display_name"];
	        this.state = source["state"];
	        this.attempts = source["attempts"];
	        this.last_error = source["last_error"];
	        this.started_at = this.convertValues(source["started_at"], null);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class Field {
	    key: string;
	    type: string;
	    description: string;
	    required: boolean;
	    secret: boolean;
	    is_set: boolean;
	    value: string;
	
	    static createFrom(source: any = {}) {
	        return new Field(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.key = source["key"];
	        this.type = source["type"];
	        this.description = source["description"];
	        this.required = source["required"];
	        this.secret = source["secret"];
	        this.is_set = source["is_set"];
	        this.value = source["value"];
	    }
	}

}

