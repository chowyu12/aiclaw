package main

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"
)

// allowed is the complete surface the host may invoke.
//
// This list exists because the Wails shell bound a struct and exposed every
// exported method on it automatically — adding a method silently widened what
// the renderer could reach, with no review point. Here a method is unreachable
// until its name appears below.
var allowed = []string{
	// providers and models
	"AddProvider", "AddProviderModel", "DeleteProvider", "Providers",
	"RemoveProviderModel", "SyncProviderModels",
	"LastModelSelection", "SetLastModelSelection",
	// conversations
	"ArchiveThread", "BackgroundChats", "Chat", "Retry", "StartChat",
	"StartRetry", "ThreadMessages", "Threads",
	// projects
	"CreateProject", "DeleteProject", "MoveThreadToProject", "Projects",
	// attachments and files
	"ChooseAttachments", "DiscardAttachment", "ImportAttachments",
	"OpenAttachment", "OpenOutputFile", "RevealAttachment", "RevealOutputFile",
	// search
	"AddSearchEngine", "DeleteSearchEngine", "SearchEngines", "ToggleSearchEngine",
	// memory
	"ApproveMemory", "ForgetMemory", "Memories", "MemorySettings", "SetMemorySettings",
	// MCP
	"AddMCPServer", "DeleteMCPServer", "MCPServers", "ToggleMCPServer",
	// plugins
	"ChoosePluginDirectory", "DeletePlugin", "PluginConfigFields", "Plugins",
	"SetPluginConfig", "TogglePlugin",
	// inbound channels
	"AuthorizeChannelBinding", "ChannelBindings", "ChannelStatus",
	"RevokeChannelBinding",
	// WeChat sign-in
	"PollWeChatLogin", "StartWeChatLogin",
	// diagnostics
	"Status",
}

// Dispatcher resolves command names against a target's methods.
//
// Arguments are decoded by reflection rather than by 53 hand-written decoders:
// the allowlist above is what makes the surface explicit, and mechanical JSON
// to argument conversion is far less error-prone done once than transcribed
// fifty-three times.
type Dispatcher struct {
	target  reflect.Value
	methods map[string]reflect.Value
}

// NewDispatcher binds the allowlist to a target, failing when a listed name is
// not a method on it. A typo in the list is a wiring mistake and must not wait
// until a user clicks the button that needs it.
func NewDispatcher(target any) (*Dispatcher, error) {
	value := reflect.ValueOf(target)
	dispatcher := &Dispatcher{target: value, methods: make(map[string]reflect.Value, len(allowed))}
	var missing []string
	for _, name := range allowed {
		method := value.MethodByName(name)
		if !method.IsValid() {
			missing = append(missing, name)
			continue
		}
		dispatcher.methods[name] = method
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return nil, fmt.Errorf("allowlisted commands are not methods on the target: %s", strings.Join(missing, ", "))
	}
	return dispatcher, nil
}

// Commands lists the callable names, for the handshake.
func (d *Dispatcher) Commands() []string {
	names := make([]string, 0, len(d.methods))
	for name := range d.methods {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// Call invokes one command.
func (d *Dispatcher) Call(command string, params []json.RawMessage) (any, error) {
	method, ok := d.methods[strings.TrimSpace(command)]
	if !ok {
		return nil, fmt.Errorf("command %q is not available", command)
	}
	kind := method.Type()
	if kind.NumIn() != len(params) {
		return nil, fmt.Errorf("command %q takes %d arguments, got %d", command, kind.NumIn(), len(params))
	}
	args := make([]reflect.Value, 0, len(params))
	for index := range params {
		argument := reflect.New(kind.In(index))
		if err := json.Unmarshal(params[index], argument.Interface()); err != nil {
			return nil, fmt.Errorf("argument %d of %q: %w", index+1, command, err)
		}
		args = append(args, argument.Elem())
	}
	return interpret(command, method.Call(args))
}

// interpret turns a method's return values into a result and an error. Every
// bound method returns nothing, a value, an error, or a value and an error.
func interpret(command string, out []reflect.Value) (any, error) {
	var result any
	var failure error
	for _, value := range out {
		if value.Type() == errorType {
			if !value.IsNil() {
				failure = value.Interface().(error)
			}
			continue
		}
		if result != nil {
			return nil, fmt.Errorf("command %q returns more than one value", command)
		}
		result = value.Interface()
	}
	return result, failure
}

var errorType = reflect.TypeOf((*error)(nil)).Elem()
