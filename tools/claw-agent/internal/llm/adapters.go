package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"

	"github.com/chowyu12/aiclaw/internal/model"
)

// An adapter owns wire semantics, not retries, execution, approval or persistence.
type adapter struct {
	encode        func(*Client, Request) (string, any, error)
	decode        func(io.Reader, func(Delta)) (Response, error)
	authorize     func(*http.Request, string)
	catalogPath   string
	decodeCatalog func([]byte) ([]string, error)
	media         *mediaOperations
}

func bearer(req *http.Request, key string) { req.Header.Set("Authorization", "Bearer "+key) }
func anthropicAuth(req *http.Request, key string) {
	req.Header.Set("x-api-key", key)
	req.Header.Set("anthropic-version", "2023-06-01")
}
func geminiAuth(req *http.Request, key string) { req.Header.Set("x-goog-api-key", key) }

var adapters = map[model.APIProtocol]adapter{
	model.APIChatCompletions: {
		encode: func(c *Client, req Request) (string, any, error) {
			wire := buildWireRequest(req)
			for i, m := range req.Messages {
				if m.Role == RoleAssistant && c.replayable(m.ProviderState, req.Model) {
					var state struct {
						Reasoning string `json:"reasoning_content"`
					}
					if json.Unmarshal(m.ProviderState.Output[0], &state) == nil {
						wire.Messages[i].ReasoningContent = &state.Reasoning
					}
				}
			}
			if req.DisableThinking {
				c.disableThinking(&wire)
			}
			return "/chat/completions", wire, nil
		}, decode: consumeStream, authorize: bearer, catalogPath: "/models", decodeCatalog: decodeOpenAICatalog, media: &openAIMedia,
	},
	model.APIResponses: {encode: func(c *Client, req Request) (string, any, error) { return "/responses", c.responsesRequest(req), nil }, decode: consumeResponses, authorize: bearer, catalogPath: "/models", decodeCatalog: decodeOpenAICatalog, media: &openAIMedia},
	model.APIAnthropic: {encode: encodeAnthropic, decode: consumeAnthropic, authorize: anthropicAuth, catalogPath: "/models?limit=1000", decodeCatalog: decodeOpenAICatalog},
	model.APIGemini:    {encode: encodeGemini, decode: consumeGemini, authorize: geminiAuth, catalogPath: "/models?pageSize=1000", decodeCatalog: decodeGeminiCatalog, media: &geminiMedia},
}

// ProviderState contains only native output from a completed sample. It is not
// visible conversation content and is never interpreted by the Agent layer.
type ProviderState struct {
	ProviderID int64             `json:"providerId,omitempty"`
	Protocol   model.APIProtocol `json:"protocol"`
	BaseURL    string            `json:"baseUrl"`
	Model      string            `json:"model"`
	Output     []json.RawMessage `json:"output"`
}

func (c *Client) replayable(state *ProviderState, modelName string) bool {
	return state != nil && state.ProviderID == c.providerID && state.Protocol == c.protocol && state.BaseURL == c.baseURL && state.Model == modelName && len(state.Output) > 0
}

func (c *Client) CatalogRequest(ctx context.Context) (*http.Request, error) {
	req, err := c.request(ctx, http.MethodGet, c.adapter.catalogPath, nil)
	if err == nil {
		req.Header.Set("Accept", "application/json")
	}
	return req, err
}

func (c *Client) DecodeCatalog(raw []byte) ([]string, error) { return c.adapter.decodeCatalog(raw) }

func (c *Client) fetchCatalog(ctx context.Context) ([]byte, error) {
	req, err := c.CatalogRequest(ctx)
	if err != nil {
		return nil, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, &Error{Message: "Model catalog request failed", Err: err}
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, readErrorBody(resp)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, (4<<20)+1))
	if err != nil {
		return nil, err
	}
	if len(raw) > 4<<20 {
		return nil, fmt.Errorf("model catalog exceeds 4 MiB")
	}
	return raw, nil
}

func (c *Client) ListModels(ctx context.Context) ([]string, error) {
	raw, err := c.fetchCatalog(ctx)
	if err != nil {
		return nil, err
	}
	return c.DecodeCatalog(raw)
}

// Only explicit gateway metadata produces supported/unsupported values.
func (c *Client) ListModelsAndMetadata(ctx context.Context) ([]string, map[string]model.ModelCapabilities, error) {
	raw, err := c.fetchCatalog(ctx)
	if err != nil {
		return nil, nil, err
	}
	names, err := c.DecodeCatalog(raw)
	if err != nil {
		return nil, nil, err
	}
	var catalog struct {
		Data []struct {
			ID           string    `json:"id"`
			Parameters   *[]string `json:"supported_parameters"`
			Architecture struct {
				Inputs *[]string `json:"input_modalities"`
			} `json:"architecture"`
		} `json:"data"`
	}
	if err = json.Unmarshal(raw, &catalog); err != nil {
		return nil, nil, err
	}
	caps := map[string]model.ModelCapabilities{}
	for _, entry := range catalog.Data {
		cap := model.ModelCapabilities{}
		if entry.Parameters != nil {
			cap.Tools = "unsupported"
			cap.Reasoning = "unsupported"
			for _, param := range *entry.Parameters {
				if param == "tools" {
					cap.Tools = "supported"
				}
				if param == "reasoning" || param == "reasoning_effort" {
					cap.Reasoning = "supported"
				}
			}
		}
		if entry.Architecture.Inputs != nil {
			cap.Vision = "unsupported"
			cap.AudioInput = "unsupported"
			for _, input := range *entry.Architecture.Inputs {
				if input == "audio" {
					cap.AudioInput = "supported"
				}
				if input == "image" {
					cap.Vision = "supported"
				}
			}
		}
		if cap != (model.ModelCapabilities{}) {
			caps[entry.ID] = cap
		}
	}
	return names, caps, nil
}

func decodeOpenAICatalog(raw []byte) ([]string, error) {
	var data struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &data); err != nil {
		return nil, err
	}
	if data.Data == nil {
		return nil, fmt.Errorf("missing model catalog")
	}
	var names []string
	for _, m := range data.Data {
		names = append(names, m.ID)
	}
	return uniqueModelNames(names), nil
}

func decodeGeminiCatalog(raw []byte) ([]string, error) {
	var data struct {
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	if err := json.Unmarshal(raw, &data); err != nil {
		return nil, err
	}
	if data.Models == nil {
		return nil, fmt.Errorf("missing model catalog")
	}
	var names []string
	for _, m := range data.Models {
		names = append(names, strings.TrimPrefix(m.Name, "models/"))
	}
	return uniqueModelNames(names), nil
}

func uniqueModelNames(items []string) []string {
	names := []string{}
	seen := map[string]bool{}
	for _, name := range items {
		name = strings.TrimSpace(name)
		if name != "" && !seen[name] {
			names = append(names, name)
			seen[name] = true
		}
	}
	sort.Strings(names)
	return names
}
