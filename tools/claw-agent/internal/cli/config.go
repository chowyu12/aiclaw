package cli

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/appdb"
	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/protocol"
)

type options struct {
	mode                                       string
	args                                       []string
	json, last                                 bool
	cwd, dataHome, appDB, config, model        string
	provider                                   int64
	appDBSet                                   bool
	providerType, baseURL, apiProtocol, models string
}

func parse(args []string) (options, error) {
	var o options
	o.mode = "chat"
	if len(args) > 0 {
		switch args[0] {
		case "exec", "resume", "config", "providers", "models", "sessions":
			o.mode, args = args[0], args[1:]
		}
	}
	flags := flag.NewFlagSet("aiclaw "+o.mode, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.StringVar(&o.cwd, "cwd", "", "working directory")
	flags.StringVar(&o.dataHome, "data-home", "", "CLI session directory")
	flags.StringVar(&o.appDB, "app-db", "", "model configuration database")
	flags.StringVar(&o.config, "config", "", "preferred model configuration JSON")
	flags.StringVar(&o.model, "model", "", "model ID")
	flags.Int64Var(&o.provider, "provider", 0, "model provider ID")
	flags.BoolVar(&o.json, "json", false, "emit JSONL events")
	flags.BoolVar(&o.last, "last", false, "resume the most recent session in this directory")
	if o.mode == "providers" {
		flags.StringVar(&o.providerType, "type", "openai-compatible", "provider type")
		flags.StringVar(&o.baseURL, "base-url", "", "provider endpoint")
		flags.StringVar(&o.apiProtocol, "api-protocol", "", "provider API protocol")
		flags.StringVar(&o.models, "models", "", "comma-separated model IDs")
	}
	for len(args) != 0 {
		arg := args[0]
		args = args[1:]
		if arg == "--" {
			o.args = append(o.args, args...)
			break
		}
		if arg == "-h" || arg == "--help" {
			return o, flag.ErrHelp
		}
		if arg == "-" || !strings.HasPrefix(arg, "-") {
			o.args = append(o.args, arg)
			continue
		}
		name, value, hasValue := strings.Cut(strings.TrimLeft(arg, "-"), "=")
		option := flags.Lookup(name)
		if option == nil {
			return o, fmt.Errorf("unknown option: %s", arg)
		}
		if !hasValue {
			if _, ok := option.Value.(interface{ IsBoolFlag() bool }); ok {
				value = "true"
			} else if len(args) != 0 {
				value, args = args[0], args[1:]
			} else {
				return o, fmt.Errorf("--%s requires a value", name)
			}
		}
		if err := flags.Set(name, value); err != nil {
			return o, err
		}
	}
	if o.json && o.mode != "exec" {
		return o, errors.New("--json requires exec")
	}
	flags.Visit(func(f *flag.Flag) {
		if f.Name == "app-db" {
			o.appDBSet = true
		}
	})
	if o.last && o.mode != "resume" {
		return o, errors.New("--last requires resume")
	}
	if o.provider < 0 {
		return o, errors.New("--provider must be a positive ID")
	}
	if o.mode == "resume" {
		if (!o.last && len(o.args) != 1) || (o.last && len(o.args) != 0) {
			return o, errors.New("use aiclaw resume <session-id> or aiclaw resume --last")
		}
		if o.model != "" || o.provider != 0 {
			return o, errors.New("resume uses the session's saved model")
		}
	}
	return o, nil
}

func (o *options) paths() ([]string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	appHome := filepath.Join(home, ".aiclaw")
	if o.dataHome == "" {
		o.dataHome = filepath.Join(appHome, "cli")
	}
	if !o.appDBSet && o.appDB == "" {
		o.appDB = filepath.Join(o.dataHome, "aiclaw.db")
	}
	if o.config == "" {
		o.config = filepath.Join(o.dataHome, "config.json")
	}
	configDir, err := os.UserConfigDir()
	if err == nil {
		desktop := filepath.Join(configDir, "aiclaw")
		if info, err := os.Stat(desktop); err == nil && info.IsDir() {
			// Keep both the desktop's sessions and credentials outside agent file tools.
			configDir = desktop
		} else {
			configDir = ""
		}
	} else {
		configDir = ""
	}
	for _, p := range []*string{&o.cwd, &o.dataHome, &o.appDB, &o.config} {
		if *p == "" && p != &o.cwd {
			continue
		}
		*p, err = filepath.Abs(*p)
		if err != nil {
			return nil, err
		}
	}
	o.cwd, err = filepath.EvalSymlinks(o.cwd)
	if err != nil {
		return nil, fmt.Errorf("workspace: %w", err)
	}
	info, err := os.Stat(o.cwd)
	if err != nil || !info.IsDir() {
		return nil, fmt.Errorf("workspace is not a directory: %s", o.cwd)
	}
	if configDir != "" && sameDirectory(o.dataHome, filepath.Join(configDir, "agent-home")) {
		return nil, errors.New("choose a CLI session directory separate from the desktop session store")
	}

	protected := []string{appHome, o.dataHome}
	if configDir != "" {
		protected = append(protected, configDir)
	}
	if o.appDB != "" {
		protected = append(protected, o.appDB, o.appDB+"-wal", o.appDB+"-shm", o.appDB+"-journal", appdb.KeyFile(o.appDB))
	}
	if o.config != "" {
		protected = append(protected, o.config)
	}
	return protected, nil
}

func lockDataHome(ctx context.Context, path string) (func(), error) {
	if err := os.MkdirAll(path, 0o700); err != nil {
		return nil, err
	}
	// This transaction is on a separate database so it cannot block the agent's own writes.
	// SQLite releases its OS lock when the process exits, including a forced exit.
	location := filepath.ToSlash(filepath.Join(path, "cli-lock.db"))
	if !strings.HasPrefix(location, "/") {
		location = "/" + location
	}
	dsn := (&url.URL{Scheme: "file", Path: location, RawQuery: "_pragma=busy_timeout(0)"}).String()
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err := db.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		db.Close()
		return nil, fmt.Errorf("cannot lock CLI session directory (another CLI may be using it): %w", err)
	}
	return func() { _ = db.Close() }, nil
}

func chooseModel(ctx context.Context, c *connection, o options, apiKey string) (protocol.ModelConfig, error) {
	var preferred protocol.ModelConfig
	if o.config != "" {
		if raw, err := os.ReadFile(o.config); err == nil {
			// The model key stays in SQLite and is resolved by the kernel.
			_ = json.Unmarshal(raw, &preferred)
		}
	}
	if o.provider == 0 && apiKey != "" {
		model := o.model
		if model == "" {
			model = strings.TrimSpace(os.Getenv("AICLAW_MODEL"))
		}
		endpoint := strings.TrimSpace(os.Getenv("AICLAW_BASE_URL"))
		if model != "" && endpoint != "" {
			return protocol.ModelConfig{Model: model, BaseURL: endpoint}, nil
		}
	}
	if o.appDB == "" {
		return protocol.ModelConfig{}, errors.New("set AICLAW_MODEL, AICLAW_BASE_URL and AICLAW_LLM_KEY when running without a model database")
	}
	var catalog protocol.ProviderListResult
	if err := c.call(ctx, protocol.MethodProviderList, struct{}{}, &catalog); err != nil {
		return protocol.ModelConfig{}, err
	}
	match := func(id int64, model string) (protocol.ModelConfig, bool) {
		for _, provider := range catalog.Providers {
			if !provider.Enabled || !provider.APIKeySet || id != 0 && provider.ID != id {
				continue
			}
			if model != "" && id != 0 {
				return protocol.ModelConfig{ProviderID: provider.ID, Model: model}, true
			}
			for _, entry := range provider.Models {
				name := protocol.ParseModelMark(entry).Name
				if name != "" && (model == "" || name == model) {
					return protocol.ModelConfig{ProviderID: provider.ID, Model: name}, true
				}
			}
		}
		return protocol.ModelConfig{}, false
	}
	if o.provider == 0 && o.model == "" && preferred.ProviderID != 0 && preferred.Model != "" {
		if _, ok := match(preferred.ProviderID, preferred.Model); ok {
			return preferred, nil
		}
	}
	if model, ok := match(o.provider, o.model); ok {
		return model, nil
	}
	return protocol.ModelConfig{}, errors.New("no matching model; run aiclaw providers add to configure one, or check aiclaw models list")
}

func recentSession(ctx context.Context, c *connection, directory string) (string, error) {
	var result struct {
		Sessions []struct{ ID, Workdir, ParentID string }
	}
	if err := c.call(ctx, protocol.MethodSessionList, struct{}{}, &result); err != nil {
		return "", err
	}
	for _, session := range result.Sessions {
		if session.ParentID != "" {
			continue
		}
		path, err := filepath.EvalSymlinks(session.Workdir)
		if err == nil && sameDirectory(path, directory) {
			return session.ID, nil
		}
	}
	return "", errors.New("no CLI session found for this directory")
}

func sameDirectory(a, b string) bool {
	if a == b {
		return true
	}
	first, firstErr := os.Stat(a)
	second, secondErr := os.Stat(b)
	return firstErr == nil && secondErr == nil && os.SameFile(first, second)
}
