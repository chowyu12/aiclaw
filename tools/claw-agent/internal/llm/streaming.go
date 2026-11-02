package llm

import (
	"bufio"
	"encoding/json"
	"io"
	"strings"
)

// readEvents preserves SSE event boundaries, including multiline data. A provider
// must acknowledge completion; a clean socket EOF is not an inference completion.
func readEvents(body io.Reader, handle func(json.RawMessage) (bool, error)) error {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	var data strings.Builder
	dispatch := func() (bool, error) {
		if data.Len() == 0 {
			return false, nil
		}
		raw := strings.TrimSpace(data.String())
		data.Reset()
		if raw == "[DONE]" {
			return false, nil
		}
		return handle(json.RawMessage(raw))
	}
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			done, err := dispatch()
			if done || err != nil {
				return err
			}
			continue
		}
		if strings.HasPrefix(line, "data:") {
			if data.Len() > 0 {
				data.WriteByte('\n')
			}
			data.WriteString(strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
			if data.Len() > 8*1024*1024 {
				return &Error{Code: "response_incomplete", Message: "Model stream event exceeds 8 MiB"}
			}
		}
	}
	if scanner.Err() != nil {
		return &Error{Message: "Failed to read model stream", Err: scanner.Err()}
	}
	done, err := dispatch()
	if done || err != nil {
		return err
	}
	return &Error{Message: "Model stream ended before completion; no tool calls were executed", Err: io.ErrUnexpectedEOF}
}

func incompleteNative(reason string) error {
	return &Error{Code: "response_incomplete", Message: "Model response incomplete (" + reason + "); no tool calls were executed"}
}

// All decoders use this boundary: partial text survives, native state and tool
// calls are committed only after the protocol's terminal acknowledgement.
func finishSample(response *Response, text *strings.Builder, err *error) {
	response.Content = text.String()
	if *err != nil {
		response.ToolCalls = nil
		response.ProviderState = nil
	}
}

func mergeContent(messages *[]map[string]any, role string, parts []any) {
	if len(parts) == 0 {
		return
	}
	items := *messages
	if len(items) > 0 && items[len(items)-1]["role"] == role {
		items[len(items)-1]["content"] = append(items[len(items)-1]["content"].([]any), parts...)
	} else {
		*messages = append(items, map[string]any{"role": role, "content": parts})
	}
}
