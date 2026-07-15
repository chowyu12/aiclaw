package agent

import (
	"github.com/chowyu12/aiclaw/internal/model"
	harnesspkg "github.com/chowyu12/aiclaw/pkg/harness"
)

type streamHarnessSink struct {
	dispatcher     *streamDispatcher
	runID          string
	conversationID string
}

func (s *streamHarnessSink) Emit(evt harnesspkg.Event) error {
	if s == nil || s.dispatcher == nil {
		return nil
	}
	return s.dispatcher.Emit(model.StreamChunk{
		RunID:          s.runID,
		ConversationID: s.conversationID,
		HarnessEvent:   evt,
	})
}

type harnessSinkFanout []harnesspkg.Sink

func fanoutHarnessSinks(sinks ...harnesspkg.Sink) harnesspkg.Sink {
	filtered := make(harnessSinkFanout, 0, len(sinks))
	for _, sink := range sinks {
		if sink != nil {
			filtered = append(filtered, sink)
		}
	}
	return filtered
}

func (s harnessSinkFanout) Emit(evt harnesspkg.Event) error {
	for _, sink := range s {
		if err := sink.Emit(evt); err != nil {
			return err
		}
	}
	return nil
}
