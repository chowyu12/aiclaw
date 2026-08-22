// Package appserver is the local command-processing boundary modelled after
// Codex app-server. It has no HTTP transport: a terminal app, IDE bridge or
// stdio transport can submit protocol commands and consume protocol events.
package appserver

import (
	"context"
	"fmt"

	"github.com/chowyu12/aiclaw/internal/core"
	"github.com/chowyu12/aiclaw/internal/model"
	"github.com/chowyu12/aiclaw/internal/protocol"
)

type localStore interface {
	core.ThreadStore
	CreateThread(context.Context, *model.Thread) error
	ArchiveThread(context.Context, int64) error
}
type Service struct {
	store   localStore
	sampler core.Sampler
	tools   core.ToolDispatcher
}

func New(s localStore, sampler core.Sampler, tools core.ToolDispatcher) *Service {
	return &Service{store: s, sampler: sampler, tools: tools}
}

func (s *Service) Handle(ctx context.Context, command protocol.Command, emit core.EventSink) error {
	switch command.Kind {
	case protocol.CommandCreateThread:
		if command.ProviderID == 0 || command.ModelName == "" {
			return fmt.Errorf("provider and model are required")
		}
		thread := &model.Thread{
			UserID: command.UserID, ProjectUUID: command.ProjectUUID,
			ProviderID: command.ProviderID, ModelName: command.ModelName,
			SearchEngineID: command.SearchEngineID, WorkingDir: command.WorkingDir,
			Title: command.Input,
		}
		if err := s.store.CreateThread(ctx, thread); err != nil {
			return err
		}
		if emit != nil {
			return emit(protocol.Event{Kind: protocol.EventThreadCreated, ThreadID: thread.UUID})
		}
		return nil
	case protocol.CommandStartTurn:
		session, err := core.Resume(ctx, s.store, command.ThreadID, emit)
		if err != nil {
			return err
		}
		return session.RunTurnWithAttachments(ctx, command.Input, command.Attachments, s.sampler, s.tools)
	case protocol.CommandResumeThread:
		_, err := core.Resume(ctx, s.store, command.ThreadID, emit)
		return err
	case protocol.CommandArchiveThread:
		thread, err := s.store.GetThreadByUUID(ctx, command.ThreadID, false)
		if err != nil {
			return err
		}
		return s.store.ArchiveThread(ctx, thread.ID)
	default:
		return fmt.Errorf("unsupported command %q", command.Kind)
	}
}
