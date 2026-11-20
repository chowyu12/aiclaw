package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/chowyu12/aiclaw/internal/model"
	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/protocol"
)

func runSettings(ctx context.Context, c *connection, o options, apiKey string, out io.Writer) error {
	action := strings.Join(o.args, " ")
	switch o.mode {
	case "config":
		if action != "show" {
			return errors.New("use aiclaw config show")
		}
		var preferred protocol.ModelConfig
		raw, err := os.ReadFile(o.config)
		if err == nil {
			err = json.Unmarshal(raw, &preferred)
		}
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return json.NewEncoder(out).Encode(map[string]any{
			"config": o.config, "provider_db": o.appDB, "session_db": filepath.Join(o.dataHome, "sessions.db"),
			"provider_id": preferred.ProviderID, "model": preferred.Model,
		})
	case "sessions":
		if action != "list" {
			return errors.New("use aiclaw sessions list")
		}
		var result struct {
			Sessions []struct{ ID, Title, Workdir string }
		}
		if err := c.call(ctx, protocol.MethodSessionList, struct{}{}, &result); err != nil {
			return err
		}
		for _, session := range result.Sessions {
			fmt.Fprintf(out, "%s\t%s\t%s\n", session.ID, terminalText(session.Title), terminalText(session.Workdir))
		}
		return nil
	}
	if o.appDB == "" {
		return errors.New("this command requires a model database")
	}
	if o.mode == "providers" && len(o.args) == 2 && o.args[0] == "add" {
		if apiKey == "" {
			return errors.New("set AICLAW_LLM_KEY to the provider's API key")
		}
		var models []string
		for _, entry := range strings.Split(o.models, ",") {
			if name := strings.TrimSpace(entry); name != "" {
				models = append(models, name)
			}
		}
		if len(models) == 0 {
			return errors.New("--models requires at least one model ID")
		}
		var provider protocol.ProviderView
		err := c.call(ctx, protocol.MethodProviderCreate, protocol.ProviderCreateParams{
			Name: o.args[1], Type: o.providerType, BaseURL: strings.TrimSpace(o.baseURL),
			APIKey: apiKey, Models: models, APIProtocol: model.APIProtocol(o.apiProtocol),
		}, &provider)
		if err != nil {
			return err
		}
		fmt.Fprintf(out, "%d\t%s\n", provider.ID, terminalText(provider.Name))
		return nil
	}

	var result protocol.ProviderListResult
	if err := c.call(ctx, protocol.MethodProviderList, struct{}{}, &result); err != nil {
		return err
	}
	switch {
	case o.mode == "providers" && action == "list":
		for _, provider := range result.Providers {
			fmt.Fprintf(out, "%d\t%s\t%s\tenabled=%t\tkey_set=%t\n",
				provider.ID, terminalText(provider.Name), terminalText(provider.Type), provider.Enabled, provider.APIKeySet)
		}
		return nil
	case o.mode == "models" && action == "list":
		for _, provider := range result.Providers {
			for _, entry := range provider.Models {
				fmt.Fprintf(out, "%d\t%s\n", provider.ID, terminalText(protocol.ParseModelMark(entry).Name))
			}
		}
		return nil
	case o.mode == "models" && action == "use":
		if o.provider <= 0 || strings.TrimSpace(o.model) == "" {
			return errors.New("use aiclaw models use --provider ID --model MODEL")
		}
		for _, provider := range result.Providers {
			if provider.ID != o.provider || !provider.Enabled || !provider.APIKeySet {
				continue
			}
			for _, entry := range provider.Models {
				if protocol.ParseModelMark(entry).Name != o.model {
					continue
				}
				if err := saveModel(o.config, o.provider, o.model); err != nil {
					return err
				}
				fmt.Fprintf(out, "%d\t%s\n", o.provider, terminalText(o.model))
				return nil
			}
		}
		return errors.New("model is not configured on an enabled provider with a key")
	default:
		return errors.New("use aiclaw providers list, providers add, models list or models use")
	}
}

func saveModel(path string, provider int64, modelID string) error {
	config := map[string]json.RawMessage{}
	data, err := os.ReadFile(path)
	if err == nil {
		if err = json.Unmarshal(data, &config); err != nil {
			return err
		}
		if config == nil {
			return errors.New("model config must be a JSON object")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	config["providerId"], _ = json.Marshal(provider)
	config["model"], _ = json.Marshal(modelID)
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return err
	}
	draft, err := os.CreateTemp(directory, "config-*.json")
	if err != nil {
		return err
	}
	defer os.Remove(draft.Name())
	encoder := json.NewEncoder(draft)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(config); err != nil {
		_ = draft.Close()
		return err
	}
	if err := draft.Close(); err != nil {
		return err
	}
	return os.Rename(draft.Name(), path)
}
