package plugin

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/chowyu12/aiclaw/internal/protocol"
)

// Inbound is one message arriving from an external conversation.
//
// Everything in it is untrusted data. It reaches a turn only through Gateway,
// which decides whether this external conversation may run one at all.
type Inbound struct {
	// ChannelID is the contributing channel, e.g. "wecom".
	ChannelID string
	// ExternalKey identifies the remote conversation: a group chat id where
	// there is one, otherwise the sender.
	ExternalKey string
	// DisplayName is a human label for the authorization prompt.
	DisplayName string
	SenderID    string
	// Text is the normalized, user-visible message body.
	Text string
	// Attachments are UUIDs of files already downloaded into the file store.
	Attachments []string
	// Images are the decoded bytes of images the sender attached (JPEG/PNG/…).
	// The channel downloads and decrypts them; the gateway scales them down and
	// hands them to the turn, where a model that cannot see images gets a
	// description from the vision model instead.
	Images [][]byte
	// Files are other attachments the sender sent, already downloaded.
	Files []InboundFile
}

// InboundFile is one downloaded attachment.
type InboundFile struct {
	Name string
	Data []byte
}

// Gateway turns an inbound message into a turn. It is implemented by the
// application server, so a channel never touches a session directly.
//
// Submit reports ErrBindingNotAllowed when the external conversation has not
// been authorized; a channel should tell the sender that approval is pending
// rather than retrying.
type Gateway interface {
	Submit(ctx context.Context, pluginUUID string, message Inbound, observe func(protocol.Event) error) error
}

// ErrBindingNotAllowed reports an inbound message from a conversation the user
// has not authorized.
var ErrBindingNotAllowed = fmt.Errorf("this conversation is not authorized to reach the agent")

// ChannelDeps is what a running channel is given.
type ChannelDeps struct {
	// PluginUUID identifies the bundle this channel belongs to.
	PluginUUID string
	// Config reads the plugin's stored configuration, secrets included.
	Config ConfigReader
	// Gateway submits inbound messages as turns.
	Gateway Gateway
	// Log records channel activity. It must never be given a secret.
	Log func(format string, args ...any)
}

// Channel is a long-running inbound connector.
type Channel interface {
	// ID matches the manifest's channel contribution id.
	ID() string
	// Run blocks until ctx is cancelled or an unrecoverable error occurs.
	// Returning a non-nil error causes the host to restart it with backoff.
	Run(ctx context.Context, deps ChannelDeps) error
}

// ChannelFactory constructs a channel for a bundled provider.
type ChannelFactory func() Channel

// ChannelState is the coarse lifecycle a settings page shows.
type ChannelState string

const (
	ChannelStarting ChannelState = "starting"
	ChannelRunning  ChannelState = "running"
	ChannelRetrying ChannelState = "retrying"
	// ChannelFailed means the host gave up restarting; the user must toggle
	// the plugin to try again.
	ChannelFailed  ChannelState = "failed"
	ChannelStopped ChannelState = "stopped"
)

// ChannelStatus reports one channel's health.
type ChannelStatus struct {
	PluginUUID  string       `json:"plugin_uuid"`
	PluginName  string       `json:"plugin_name"`
	ChannelID   string       `json:"channel_id"`
	DisplayName string       `json:"display_name,omitzero"`
	State       ChannelState `json:"state"`
	Attempts    int          `json:"attempts,omitzero"`
	LastError   string       `json:"last_error,omitzero"`
	StartedAt   time.Time    `json:"started_at,omitzero"`
}

// statusHolder is the mutable half of a supervised channel.
type statusHolder struct {
	mu     sync.Mutex
	status ChannelStatus
}

func (h *statusHolder) set(mutate func(*ChannelStatus)) {
	h.mu.Lock()
	defer h.mu.Unlock()
	mutate(&h.status)
}

func (h *statusHolder) get() ChannelStatus {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.status
}

// channelKey identifies a supervised channel across sync calls.
func channelKey(pluginUUID, channelID string) string {
	return strings.TrimSpace(pluginUUID) + "/" + strings.TrimSpace(channelID)
}
