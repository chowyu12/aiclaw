package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"unicode"

	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/protocol"
)

type event struct {
	Type      string          `json:"type"`
	SessionID string          `json:"session_id,omitempty"`
	TurnID    string          `json:"turn_id,omitempty"`
	ItemID    string          `json:"item_id,omitempty"`
	Model     string          `json:"model,omitempty"`
	Text      string          `json:"text,omitempty"`
	Delta     string          `json:"delta,omitempty"`
	Tool      string          `json:"tool,omitempty"`
	Failed    bool            `json:"failed,omitempty"`
	Error     string          `json:"error,omitempty"`
	ErrorCode string          `json:"error_code,omitempty"`
	Usage     *protocol.Usage `json:"usage,omitempty"`
}

type turnFailure struct{ message, code string }

func (e *turnFailure) Error() string { return e.message }

type renderer struct {
	out, stderr io.Writer
	json        *json.Encoder
}

func (r *renderer) emit(e event) error {
	if r.json != nil {
		return r.json.Encode(e)
	}
	return nil
}

// Remote model content and approval details are plain text, including in a real terminal.
func terminalText(text string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) && r != '\n' && r != '\t' {
			return -1
		}
		return r
	}, text)
}

func runTurn(ctx context.Context, c *connection, session, prompt string, output *renderer, input *terminalInput, interrupts <-chan os.Signal) error {
	id, err := c.send(protocol.MethodTurnStart, protocol.TurnStartParams{SessionID: session, Text: prompt, Mode: "queue"})
	if err != nil {
		return err
	}
	var turnID string
	var stopping error
	stopSent := false
	stop := func(reason error) error {
		if stopping == nil {
			stopping = reason
		}
		if turnID == "" || stopSent {
			return nil
		}
		stopSent = true
		_, err := c.send(protocol.MethodTurnInterrupt, protocol.TurnInterruptParams{SessionID: session, TurnID: turnID})
		return err
	}
	streamed := map[string]bool{}
	agentItems := map[string]bool{}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-interrupts:
			if stopping != nil {
				return stopping
			}
			if err := stop(ErrInterrupted); err != nil {
				return err
			}
		case message, ok := <-c.frames:
			if !ok {
				return c.disconnected()
			}
			if message.Method == "" && string(message.ID) == id {
				if message.Error != nil {
					return errors.New(message.Error.Message)
				}
				var result protocol.TurnStartResult
				if err := json.Unmarshal(message.Result, &result); err != nil {
					return err
				}
				turnID = result.TurnID
				if stopping != nil {
					if err := stop(stopping); err != nil {
						return err
					}
				}
				continue
			}
			if len(message.ID) > 0 {
				if message.Method == "" {
					continue
				}
				if message.Method == protocol.RequestApproval || message.Method == protocol.RequestUserInput {
					if input == nil {
						if turnID == "" {
							var request protocol.TurnNotification
							if err := json.Unmarshal(message.Params, &request); err != nil {
								return err
							}
							if request.SessionID == session {
								turnID = request.TurnID
							}
						}
						if err := stop(errors.New("operation requires interactive approval or input; run aiclaw in a terminal")); err != nil {
							return err
						}
						if err := c.reject(message); err != nil {
							return err
						}
						continue
					}
					if stopping != nil {
						if err := c.reject(message); err != nil {
							return err
						}
						continue
					}
					err := respondToHost(ctx, c, message, input, output.stderr, interrupts)
					if errors.Is(err, ErrInterrupted) {
						if err := stop(err); err != nil {
							return err
						}
						if err := c.reject(message); err != nil {
							return err
						}
					} else if err != nil {
						return err
					}
				} else if err := c.reject(message); err != nil {
					return err
				}
				continue
			}

			switch message.Method {
			case protocol.NotifyTurnStarted, protocol.NotifyTurnCompleted:
				var params protocol.TurnNotification
				if err := json.Unmarshal(message.Params, &params); err != nil {
					return err
				}
				if params.SessionID != session || (turnID != "" && params.TurnID != turnID) {
					continue
				}
				turnID = params.TurnID
				if message.Method == protocol.NotifyTurnStarted {
					if err := output.emit(event{Type: "turn.started", SessionID: session, TurnID: turnID}); err != nil {
						return err
					}
					if stopping != nil {
						if err := stop(stopping); err != nil {
							return err
						}
					}
					continue
				}
				if err := output.emit(event{Type: "turn.completed", SessionID: session, TurnID: turnID, Usage: params.Usage, Error: params.Error, ErrorCode: params.ErrorCode}); err != nil {
					return err
				}
				if stopping != nil {
					return stopping
				}
				if params.Error != "" {
					return &turnFailure{message: params.Error, code: params.ErrorCode}
				}
				return nil
			case protocol.NotifyItemStarted, protocol.NotifyItemCompleted:
				var params protocol.ItemNotification
				if err := json.Unmarshal(message.Params, &params); err != nil {
					return err
				}
				if params.SessionID != session || (turnID != "" && params.TurnID != turnID) {
					continue
				}
				item := params.Item
				e := event{SessionID: session, TurnID: params.TurnID, ItemID: item.ID}
				switch {
				case item.Kind == protocol.ItemAgentMessage:
					if message.Method == protocol.NotifyItemStarted {
						agentItems[item.ID] = true
						continue
					}
					e.Type, e.Text = "message.completed", item.Text
					if output.json == nil && (streamed[item.ID] || item.Text != "") {
						if !streamed[item.ID] {
							if _, err := io.WriteString(output.out, terminalText(item.Text)); err != nil {
								return err
							}
						}
						if _, err := fmt.Fprintln(output.out); err != nil {
							return err
						}
					}
					delete(agentItems, item.ID)
					delete(streamed, item.ID)
				case item.Kind == protocol.ItemToolCall:
					e.Type, e.Tool = "tool.started", item.ToolName
					if message.Method == protocol.NotifyItemCompleted {
						e.Type, e.Failed = "tool.completed", item.ToolFailed
					}
				default:
					continue
				}
				if err := output.emit(e); err != nil {
					return err
				}
			case protocol.NotifyItemDelta:
				var params protocol.ItemDeltaNotification
				if err := json.Unmarshal(message.Params, &params); err != nil {
					return err
				}
				if params.SessionID != session || !agentItems[params.ItemID] {
					continue
				}
				if output.json == nil {
					if _, err := io.WriteString(output.out, terminalText(params.Delta)); err != nil {
						return err
					}
					streamed[params.ItemID] = true
				} else if err := output.emit(event{Type: "message.delta", SessionID: session, TurnID: params.TurnID, ItemID: params.ItemID, Delta: params.Delta}); err != nil {
					return err
				}
			}
		}
	}
}

func respondToHost(ctx context.Context, c *connection, message frame, input *terminalInput, stderr io.Writer, interrupts <-chan os.Signal) error {
	if message.Method == protocol.RequestApproval {
		var request protocol.ApprovalRequestParams
		if err := json.Unmarshal(message.Params, &request); err != nil {
			return err
		}
		fmt.Fprintf(stderr, "\n%s\n%s\n", terminalText(request.Title), terminalText(request.Detail))
		if request.Cwd != "" {
			fmt.Fprintf(stderr, "Directory: %s\n", terminalText(request.Cwd))
		}
		if request.Reason != "" {
			fmt.Fprintln(stderr, terminalText(request.Reason))
		}
		fmt.Fprint(stderr, "Allow once? [y/N] ")
		answer, err := input.read(ctx, interrupts)
		if err != nil && !errors.Is(err, io.EOF) {
			return err
		}
		answer = strings.TrimSpace(answer)
		allowed := strings.EqualFold(answer, "y") || strings.EqualFold(answer, "yes")
		return c.reply(message.ID, protocol.ApprovalResponse{Approved: allowed})
	}
	var request protocol.UserInputRequestParams
	if err := json.Unmarshal(message.Params, &request); err != nil {
		return err
	}
	fmt.Fprintf(stderr, "\n%s\n", terminalText(request.Question))
	for _, option := range request.Options {
		fmt.Fprintf(stderr, "- %s %s\n", terminalText(option.Label), terminalText(option.Description))
	}
	fmt.Fprint(stderr, "Answer (blank to skip): ")
	answer, err := input.read(ctx, interrupts)
	if err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	return c.reply(message.ID, protocol.UserInputResponse{Text: answer, Skipped: strings.TrimSpace(answer) == ""})
}
