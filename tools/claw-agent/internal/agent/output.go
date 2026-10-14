package agent

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/tools"
)

type retainedOutput struct {
	Text     string `json:"text"`
	Complete bool   `json:"complete"`
}

func (s *Session) saveToolOutput(ctx context.Context, text string, complete bool) (string, error) {
	if s.db == nil {
		return "", errors.New("output storage is unavailable for this session")
	}
	text = strings.ToValidUTF8(text, "�")
	if len(text) > tools.MaxRetainedOutput {
		text = tools.OutputPrefix(text, tools.MaxRetainedOutput)
		complete = false
	}
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return "", err
	}
	id := hex.EncodeToString(nonce[:])
	err := s.db.PutState(ctx, s.ID, "tool-output:"+id, retainedOutput{Text: text, Complete: complete})
	return id, err
}
func (s *Session) registerOutputTool() error {
	return s.registry.Register(tools.Tool{
		Name: "read_tool_output", Effect: tools.EffectRead,
		Description: "Read a retained tool/command output by output_id in this session. Offsets are UTF-8 byte offsets; follow nextOffset until hasMore=false. complete=false means the capture was capped, not that the source has no more data. This never reruns the original command or grants filesystem access.",
		Schema:      json.RawMessage(`{"type":"object","properties":{"output_id":{"type":"string"},"offset":{"type":"integer","minimum":0},"limit":{"type":"integer","minimum":1,"maximum":4096}},"required":["output_id"],"additionalProperties":false}`),
		Handler: func(ctx context.Context, raw json.RawMessage, _ *tools.Env) (string, error) {
			var args struct {
				ID     string `json:"output_id"`
				Offset int    `json:"offset"`
				Limit  int    `json:"limit"`
			}
			if err := json.Unmarshal(raw, &args); err != nil {
				return "", err
			}
			if len(args.ID) != 32 {
				return "", errors.New("invalid output_id")
			}
			if _, err := hex.DecodeString(args.ID); err != nil {
				return "", errors.New("invalid output_id")
			}
			if s.db == nil {
				return "", errors.New("output storage is unavailable for this session")
			}
			var value retainedOutput
			if err := s.db.State(ctx, s.ID, "tool-output:"+args.ID, &value); err != nil {
				return "", errors.New("output_id is not available in this session")
			}
			if args.Offset < 0 || args.Offset > len(value.Text) || (args.Offset < len(value.Text) && !utf8.RuneStart(value.Text[args.Offset])) {
				return "", errors.New("offset must be a UTF-8 boundary inside the retained output")
			}
			if args.Limit == 0 {
				args.Limit = 4096
			}
			if args.Limit < 1 || args.Limit > 4096 {
				return "", errors.New("limit must be between 1 and 4096")
			}
			text := tools.OutputPrefix(value.Text[args.Offset:], args.Limit)
			if text == "" && args.Offset < len(value.Text) {
				return "", errors.New("limit is too small for the next UTF-8 character")
			}
			end := args.Offset + len(text)
			result, err := json.Marshal(map[string]any{"text": text, "offset": args.Offset, "nextOffset": end, "totalBytes": len(value.Text), "hasMore": end < len(value.Text), "complete": value.Complete})
			return string(result), err
		},
	})
}
func (s *Session) historyOutput(ctx context.Context, output string) string {
	if len(output) <= maxToolOutputBytes {
		return output
	}
	preview := truncateForHistory(output)
	id, err := s.saveToolOutput(ctx, output, true)
	if err != nil {
		return preview + "\nFull output could not be saved; do not infer absence of data from this partial result. " + err.Error()
	}
	return preview + fmt.Sprintf("\nPartial result: read_tool_output(output_id=%q, offset=0) retrieves the retained copy; follow nextOffset. Copies over 8 MiB are incomplete. Do not infer missing data without inspecting relevant pages.", id)
}
