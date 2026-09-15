package core

import (
	"context"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	openai "github.com/chowyu12/go-openai"

	"github.com/chowyu12/aiclaw/internal/config"
	"github.com/chowyu12/aiclaw/internal/model"
	"github.com/chowyu12/aiclaw/internal/store/gormstore"
	"github.com/chowyu12/aiclaw/internal/tools/result"
)

// captureDispatcher answers a fixed number of screenshot-shaped tool calls,
// each returning a real PNG on disk, then lets the turn finish.
type captureDispatcher struct {
	directory string
	produced  []string
	// nonImage returns a text file result instead, to assert that only
	// images are replayed.
	nonImage bool
}

func (d *captureDispatcher) Definitions(context.Context, model.Thread) ([]ToolDefinition, error) {
	return []ToolDefinition{{Name: "computer"}}, nil
}

func (d *captureDispatcher) Execute(_ context.Context, _ model.Thread, call ToolCall) (ToolResult, error) {
	if d.nonImage {
		path := filepath.Join(d.directory, fmt.Sprintf("notes-%d.txt", len(d.produced)))
		if err := os.WriteFile(path, []byte("plain text artifact"), 0o644); err != nil {
			return ToolResult{}, err
		}
		d.produced = append(d.produced, path)
		return ToolResult{CallID: call.ID, Name: call.Name, Content: result.NewFileResult(path, "text/plain", "notes")}, nil
	}
	path := filepath.Join(d.directory, fmt.Sprintf("screen-%d.png", len(d.produced)))
	if err := writePNG(path, 4, 2); err != nil {
		return ToolResult{}, err
	}
	d.produced = append(d.produced, path)
	return ToolResult{CallID: call.ID, Name: call.Name, Content: result.NewFileResult(path, "image/png", "screen capture")}, nil
}

// loopSampler asks for one tool call per round until the budget runs out.
type loopSampler struct {
	rounds   int
	tool     string
	requests []SamplingRequest
}

func (s *loopSampler) Sample(_ context.Context, request SamplingRequest, emit func(string) error) (SamplingResult, error) {
	s.requests = append(s.requests, request)
	if len(s.requests) <= s.rounds {
		return SamplingResult{ToolCalls: []ToolCall{
			{ID: fmt.Sprintf("call-%d", len(s.requests)), Name: s.tool, Arguments: `{"action":"screenshot"}`},
		}}, nil
	}
	if err := emit("done"); err != nil {
		return SamplingResult{}, err
	}
	return SamplingResult{Text: "done"}, nil
}

func writePNG(path string, width, height int) error {
	canvas := image.NewRGBA(image.Rect(0, 0, width, height))
	canvas.Set(0, 0, color.RGBA{B: 255, A: 255})
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	if err := png.Encode(file, canvas); err != nil {
		_ = file.Close()
		return err
	}
	return file.Close()
}

func newArtifactRuntime(t *testing.T) (context.Context, *gormstore.GormStore, *model.Thread) {
	t.Helper()
	ctx := context.Background()
	store, err := gormstore.New(config.DatabaseConfig{Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "artifacts.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	thread := &model.Thread{UserID: "local", ProviderID: 1, ModelName: "model", Title: "computer use"}
	if err := store.CreateThread(ctx, thread); err != nil {
		t.Fatal(err)
	}
	return ctx, store, thread
}

// toolImageMessages returns the replayed image messages of one request.
func toolImageMessages(request SamplingRequest) []SamplingMessage {
	var found []SamplingMessage
	for _, message := range request.Messages {
		if message.Role == "user" && strings.HasPrefix(message.Content, "Image output from tool") {
			found = append(found, message)
		}
	}
	return found
}

// A tool result is a role=tool message, which carries no image channel. An
// image a tool produced therefore has to reach the model as a following user
// message, or computer use can act on the screen without ever seeing it.
func TestToolImagesReachTheModelAsUserAttachments(t *testing.T) {
	ctx, store, thread := newArtifactRuntime(t)
	session, err := Resume(ctx, store, thread.UUID, nil)
	if err != nil {
		t.Fatal(err)
	}
	dispatcher := &captureDispatcher{directory: t.TempDir()}
	sampler := &loopSampler{rounds: 1, tool: "computer"}
	if err := session.RunTurn(ctx, "look at my screen", sampler, dispatcher); err != nil {
		t.Fatal(err)
	}

	// The first request precedes any tool call; the second must carry the image.
	if len(sampler.requests) != 2 {
		t.Fatalf("sampler rounds = %d", len(sampler.requests))
	}
	if images := toolImageMessages(sampler.requests[0]); len(images) != 0 {
		t.Fatalf("an image appeared before any tool ran: %+v", images)
	}
	replayed := toolImageMessages(sampler.requests[1])
	if len(replayed) != 1 {
		t.Fatalf("replayed image messages = %d", len(replayed))
	}
	if len(replayed[0].Attachments) != 1 || !replayed[0].Attachments[0].IsImage() {
		t.Fatalf("attachments = %+v", replayed[0].Attachments)
	}
	if replayed[0].Attachments[0].StoragePath != dispatcher.produced[0] {
		t.Fatalf("attachment points at %q, want %q", replayed[0].Attachments[0].StoragePath, dispatcher.produced[0])
	}
	// The framing has to deny the image any authority: a screen capture can
	// show text shaped like an instruction.
	if !strings.Contains(replayed[0].Content, "not a new instruction") {
		t.Fatalf("image framing does not mark the content as data: %q", replayed[0].Content)
	}
	// The tool message itself must still be present and addressed to its call.
	tools := 0
	for _, message := range sampler.requests[1].Messages {
		if message.Role == "tool" && message.ToolCallID == "call-1" {
			tools++
		}
	}
	if tools != 1 {
		t.Fatalf("tool result messages = %d", tools)
	}
}

func TestToolArtifactsAreRecordedAsThreadFiles(t *testing.T) {
	ctx, store, thread := newArtifactRuntime(t)
	session, err := Resume(ctx, store, thread.UUID, nil)
	if err != nil {
		t.Fatal(err)
	}
	dispatcher := &captureDispatcher{directory: t.TempDir()}
	if err := session.RunTurn(ctx, "capture", &loopSampler{rounds: 1, tool: "computer"}, dispatcher); err != nil {
		t.Fatal(err)
	}
	files, err := store.ListFilesByThread(ctx, thread.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 {
		t.Fatalf("thread files = %+v", files)
	}
	// Owned by the thread, so the 24h pending-attachment sweep cannot delete it.
	if files[0].ThreadID != thread.ID || files[0].FileType != model.FileTypeImage {
		t.Fatalf("artifact record = %+v", files[0])
	}
}

func TestNonImageToolArtifactsAreNotReplayed(t *testing.T) {
	ctx, store, thread := newArtifactRuntime(t)
	session, err := Resume(ctx, store, thread.UUID, nil)
	if err != nil {
		t.Fatal(err)
	}
	dispatcher := &captureDispatcher{directory: t.TempDir(), nonImage: true}
	sampler := &loopSampler{rounds: 1, tool: "computer"}
	if err := session.RunTurn(ctx, "write notes", sampler, dispatcher); err != nil {
		t.Fatal(err)
	}
	if images := toolImageMessages(sampler.requests[1]); len(images) != 0 {
		t.Fatalf("a text artifact was replayed as an image: %+v", images)
	}
	files, _ := store.ListFilesByThread(ctx, thread.ID)
	if len(files) != 0 {
		t.Fatalf("a text artifact was recorded as a visible image: %+v", files)
	}
}

// One capture per step would otherwise replay every frame on every round, so
// only the most recent captures keep their pixels.
func TestOlderToolImagesLoseTheirPixelsButKeepTheirText(t *testing.T) {
	ctx, store, thread := newArtifactRuntime(t)
	session, err := Resume(ctx, store, thread.UUID, nil)
	if err != nil {
		t.Fatal(err)
	}
	rounds := maxToolImagesInContext + 2
	dispatcher := &captureDispatcher{directory: t.TempDir()}
	sampler := &loopSampler{rounds: rounds, tool: "computer"}
	if err := session.RunTurn(ctx, "drive the screen", sampler, dispatcher); err != nil {
		t.Fatal(err)
	}

	final := sampler.requests[len(sampler.requests)-1]
	replayed := toolImageMessages(final)
	if len(replayed) != rounds {
		t.Fatalf("replayed messages = %d, want %d", len(replayed), rounds)
	}
	withPixels := 0
	for index, message := range replayed {
		if len(message.Attachments) > 0 {
			withPixels++
			continue
		}
		// A pruned message must say why, so the transcript stays readable.
		if !strings.Contains(message.Content, "Image omitted") {
			t.Fatalf("pruned message %d gives no reason: %q", index, message.Content)
		}
	}
	if withPixels != maxToolImagesInContext {
		t.Fatalf("messages carrying pixels = %d, want %d", withPixels, maxToolImagesInContext)
	}
	// The surviving images must be the newest ones.
	for _, message := range replayed[len(replayed)-maxToolImagesInContext:] {
		if len(message.Attachments) == 0 {
			t.Fatal("a recent capture was pruned instead of an older one")
		}
	}
}

// Resuming must rebuild the same visual context: the artifact UUIDs live in
// the rollout, not only in this process.
func TestResumedThreadReplaysToolImages(t *testing.T) {
	ctx, store, thread := newArtifactRuntime(t)
	session, err := Resume(ctx, store, thread.UUID, nil)
	if err != nil {
		t.Fatal(err)
	}
	dispatcher := &captureDispatcher{directory: t.TempDir()}
	if err := session.RunTurn(ctx, "capture", &loopSampler{rounds: 1, tool: "computer"}, dispatcher); err != nil {
		t.Fatal(err)
	}

	resumed, err := Resume(ctx, store, thread.UUID, nil)
	if err != nil {
		t.Fatal(err)
	}
	sampler := &loopSampler{rounds: 0, tool: "computer"}
	if err := resumed.RunTurn(ctx, "what changed?", sampler, dispatcher); err != nil {
		t.Fatal(err)
	}
	replayed := toolImageMessages(sampler.requests[0])
	if len(replayed) != 1 || len(replayed[0].Attachments) != 1 {
		t.Fatalf("a resumed thread lost its tool images: %+v", replayed)
	}
}

// A capture that vanished from disk before the next round must not break the
// turn: the tool already ran and its textual output stands.
func TestMissingOrOversizedArtifactsAreSkipped(t *testing.T) {
	ctx, store, thread := newArtifactRuntime(t)
	session, err := Resume(ctx, store, thread.UUID, nil)
	if err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(t.TempDir(), "gone.png")
	attachments, err := session.registerToolImages(ctx, "turn-1",
		result.NewFileResult(missing, "image/png", "vanished"))
	if err != nil || len(attachments) != 0 {
		t.Fatalf("attachments = %+v err = %v", attachments, err)
	}

	oversized := filepath.Join(t.TempDir(), "huge.png")
	if err := writePNG(oversized, 2, 2); err != nil {
		t.Fatal(err)
	}
	if err := os.Truncate(oversized, maxSamplingImageBytes+1); err != nil {
		t.Fatal(err)
	}
	attachments, err = session.registerToolImages(ctx, "turn-1",
		result.NewFileResult(oversized, "image/png", "too large"))
	if err != nil || len(attachments) != 0 {
		t.Fatalf("an oversized capture was attached: %+v err = %v", attachments, err)
	}
	if files, _ := store.ListFilesByThread(ctx, thread.ID); len(files) != 0 {
		t.Fatalf("a skipped artifact was still recorded: %+v", files)
	}
}

// The last link in the chain: a replayed image message must translate into a
// provider request that actually carries pixels. Without this the loop would
// look correct while sending the model nothing but text.
func TestReplayedToolImageTranslatesToAnImagePart(t *testing.T) {
	ctx, store, thread := newArtifactRuntime(t)
	session, err := Resume(ctx, store, thread.UUID, nil)
	if err != nil {
		t.Fatal(err)
	}
	dispatcher := &captureDispatcher{directory: t.TempDir()}
	sampler := &loopSampler{rounds: 1, tool: "computer"}
	if err := session.RunTurn(ctx, "look", sampler, dispatcher); err != nil {
		t.Fatal(err)
	}
	replayed := toolImageMessages(sampler.requests[1])
	if len(replayed) != 1 {
		t.Fatalf("replayed = %+v", replayed)
	}

	translated, err := translateSamplingMessage(replayed[0])
	if err != nil {
		t.Fatal(err)
	}
	images := 0
	for _, part := range translated.MultiContent {
		if part.Type == openai.ChatMessagePartTypeImageURL && part.ImageURL != nil {
			images++
			if !strings.HasPrefix(part.ImageURL.URL, "data:image/png;base64,") {
				t.Fatalf("image part is not inline PNG data: %.40q", part.ImageURL.URL)
			}
		}
	}
	if images != 1 {
		t.Fatalf("translated image parts = %d; the model would receive no pixels", images)
	}
	if translated.Role != openai.ChatMessageRoleUser {
		t.Fatalf("role = %q; only user messages carry images", translated.Role)
	}
}
