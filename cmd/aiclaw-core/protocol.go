// Command aiclaw-core is the headless application core. It speaks JSON Lines
// over stdin and stdout so a host process — the Electron shell — can drive it
// without exposing a local network port. All behaviour lives in
// internal/appservice, shared with the Wails shell.
//
// See docs/design/electron-migration.md.
package main

import "encoding/json"

// Request is one call from the host.
type Request struct {
	// ID correlates the reply and any events with this call. Concurrent
	// background turns mean replies cannot be matched by arrival order.
	ID string `json:"id"`
	// Command is the name the host wants to invoke. It must appear in the
	// allowlist; an exported method that is not listed is unreachable.
	Command string `json:"command"`
	// Params are positional arguments, matching the order of the method's
	// parameters. This mirrors the argument lists the interface already
	// passes, so the host's bridge stays a pass-through.
	Params []json.RawMessage `json:"params,omitempty"`
	// Reply carries the host's answer to a HostCall the core made. Requests
	// and replies share one pipe, so they are distinguished by this field.
	Reply *HostReply `json:"reply,omitempty"`
}

// Response is the terminal outcome of one Request.
type Response struct {
	ID     string `json:"id"`
	OK     bool   `json:"ok"`
	Result any    `json:"result,omitempty"`
	Error  string `json:"error,omitempty"`
}

// EventLine carries an out-of-band event to the host, tagged with the request
// that produced it.
type EventLine struct {
	ID string `json:"id,omitempty"`
	// Name is the event channel, e.g. "chat:delta". The names match what the
	// Wails shell emitted so the interface needs no change.
	Name string `json:"event"`
	Data []any  `json:"data,omitempty"`
}

// HostCall asks the host to do something only it can: show a native dialog.
// The core is headless and owns no window.
type HostCall struct {
	ID     string `json:"id"`
	Host   string `json:"host"`
	Params any    `json:"params,omitempty"`
}

// HostReply is the host's answer to a HostCall.
type HostReply struct {
	OK     bool            `json:"ok"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  string          `json:"error,omitempty"`
}

// Handshake is the first line the core writes. The host waits for it before
// showing a window, and refuses to continue when the protocol version does
// not match what it expects.
type Handshake struct {
	Ready    bool     `json:"ready"`
	Protocol int      `json:"protocol"`
	Commands []string `json:"commands"`
}

// ProtocolVersion is bumped when the shape of these messages changes
// incompatibly.
const ProtocolVersion = 1
