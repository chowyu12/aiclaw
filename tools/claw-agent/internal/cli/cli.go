// Package cli is the terminal host for the same local kernel used by AIClaw desktop.
package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/protocol"
	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/server"
)

var ErrInterrupted = errors.New("interrupted")

const usage = `AIClaw terminal agent

Usage:
  aiclaw [--cwd DIR] [PROMPT]
  aiclaw exec [--json] [--cwd DIR] PROMPT
  aiclaw exec --json -          Read the prompt from stdin
  aiclaw exec resume SESSION_ID [--json] PROMPT
  aiclaw exec resume --last [--cwd DIR] [--json] PROMPT
  aiclaw resume <SESSION_ID>
  aiclaw resume --last          Resume the most recent CLI session in this directory
  aiclaw sessions list          List CLI session IDs
  aiclaw sessions search QUERY [--open]  Find sessions and optionally open a match
  aiclaw sessions open ID_OR_LINK       Read a linked session, including archived sessions
  aiclaw sessions show ID       Read a saved transcript without resuming it
  aiclaw config show            Show the CLI paths and saved model selection
  aiclaw providers list
  aiclaw providers add NAME --base-url URL --models MODEL[,MODEL...]  (key: AICLAW_LLM_KEY)
  aiclaw models list
  aiclaw models use --provider ID --model MODEL
  aiclaw --version

Options:
  --model ID                    Use a configured model
  --provider ID                 Choose a configured model service
  --data-home DIR               CLI session directory (default ~/.aiclaw/cli)
  --app-db FILE                 Model database (default ~/.aiclaw/cli/aiclaw.db)
  --config FILE                 Config JSON (default ~/.aiclaw/cli/config.json)

Interactive commands: /open ID_OR_LINK, /exit, /quit. Ctrl+C interrupts the active turn.
Standalone model selection: AICLAW_BASE_URL, AICLAW_MODEL and AICLAW_LLM_KEY.
`

func Run(ctx context.Context, args []string, version string, in io.Reader, out, stderr io.Writer, interrupts <-chan os.Signal) (runErr error) {
	opts, err := parse(args)
	if errors.Is(err, flag.ErrHelp) {
		_, err = io.WriteString(out, usage)
		return err
	}
	if err != nil {
		return err
	}
	output := &renderer{out: out, stderr: stderr}
	if opts.json {
		output.json = json.NewEncoder(out)
		defer func() {
			if runErr != nil {
				failure := event{Type: "error", Error: runErr.Error()}
				var turnErr *turnFailure
				if errors.As(runErr, &turnErr) {
					failure.ErrorCode = turnErr.code
				}
				_ = output.emit(failure)
			}
		}()
	}
	var sessionID string
	if opts.resume && !opts.last {
		sessionID, opts.args = opts.args[0], opts.args[1:]
		sessionID, err = sessionTarget(sessionID)
		if err != nil {
			return err
		}
	}
	prompt := strings.Join(opts.args, " ")
	if opts.mode == "exec" {
		if prompt == "-" {
			raw, err := readPrompt(ctx, in, interrupts)
			if err != nil {
				return err
			}
			prompt = raw
		}
		if strings.TrimSpace(prompt) == "" {
			return errors.New("exec requires a prompt; use - to read stdin")
		}
	}
	protected, err := opts.paths()
	if err != nil {
		return err
	}
	unlock, err := lockDataHome(ctx, opts.dataHome)
	if err != nil {
		return err
	}
	defer unlock()

	apiKey := strings.TrimSpace(os.Getenv("AICLAW_LLM_KEY"))
	c, err := connect(server.Options{Version: version, DataHome: opts.dataHome, AppDB: opts.appDB, APIKey: apiKey, ModelsOnly: true})
	if err != nil {
		return err
	}
	defer c.close()
	if opts.mode == "sessions" {
		return runSessions(ctx, c, opts, in, out, stderr, interrupts)
	}
	if opts.mode != "chat" && opts.mode != "exec" && opts.mode != "resume" {
		return runSettings(ctx, c, opts, apiKey, out)
	}

	servers, err := cliMCPServers(opts.config)
	if err != nil {
		return err
	}
	var session protocol.SessionStartResult
	if opts.resume {
		if opts.last {
			sessionID, err = recentSession(ctx, c, opts.cwd)
		}
		if err != nil {
			return err
		}
		err = c.call(ctx, protocol.MethodSessionResume, protocol.SessionResumeParams{
			SessionID: sessionID, Refresh: &protocol.SessionRefresh{ApprovalPolicy: protocol.ApprovalAlways, MCPServers: servers},
		}, &session)
		if err != nil {
			return err
		}
	} else {
		model, err := chooseModel(ctx, c, opts, apiKey)
		if err != nil {
			return err
		}
		err = c.call(ctx, protocol.MethodSessionStart, protocol.SessionStartParams{
			Model: model, Workdir: opts.cwd, ProtectedPaths: protected,
			ApprovalPolicy: protocol.ApprovalAlways, MCPServers: servers,
		}, &session)
		if err != nil {
			return err
		}
	}
	for _, startup := range session.MCPStartup {
		if startup.Transport == "http" && startup.State == "failed" {
			fmt.Fprintf(stderr, "MCP %s: startup failed (%s)\n", terminalText(startup.Name), terminalText(startup.Reason))
		}
	}
	if opts.mode == "exec" {
		if err := output.emit(event{Type: "session.started", SessionID: session.SessionID, Model: session.Model}); err != nil {
			return err
		}
		return runTurn(ctx, c, session.SessionID, prompt, output, nil, interrupts)
	}

	lineCtx, stopInput := context.WithCancel(ctx)
	defer stopInput()
	input := newTerminalInput(lineCtx, in)
	fmt.Fprintf(stderr, "AIClaw · %s · %s\nSession %s\n", terminalText(session.Model), terminalText(session.Workspace), session.SessionID)
	if opts.mode == "resume" {
		if err := printHistory(ctx, c, session.SessionID, out); err != nil {
			return err
		}
	}
	for {
		if strings.TrimSpace(prompt) != "" {
			err := runTurn(ctx, c, session.SessionID, prompt, output, input, interrupts)
			if err != nil {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				fmt.Fprintln(stderr, terminalText(err.Error()))
			}
		}
		fmt.Fprint(stderr, "\n> ")
		prompt, err = input.read(ctx, interrupts)
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		command := strings.TrimSpace(prompt)
		if command == "/exit" || command == "/quit" {
			return nil
		}
		if target, ok := strings.CutPrefix(command, "/open "); ok {
			prompt = ""
			if err := openSessionHistory(ctx, c, strings.TrimSpace(target), out); err != nil {
				fmt.Fprintln(stderr, terminalText(err.Error()))
			}
		}
	}
}

func readPrompt(ctx context.Context, in io.Reader, interrupts <-chan os.Signal) (string, error) {
	type result struct {
		text string
		err  error
	}
	ready := make(chan result, 1)
	go func() {
		const limit = 16 * 1024 * 1024
		raw, err := io.ReadAll(io.LimitReader(in, limit+1))
		if err == nil && len(raw) > limit {
			err = errors.New("stdin exceeds the kernel's 16 MiB frame limit")
		}
		ready <- result{string(raw), err}
	}()
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case <-interrupts:
		return "", ErrInterrupted
	case value := <-ready:
		return value.text, value.err
	}
}

type terminalInput struct {
	lines chan string
	err   error
}

func newTerminalInput(ctx context.Context, in io.Reader) *terminalInput {
	t := &terminalInput{lines: make(chan string)}
	go func() {
		defer close(t.lines)
		scanner := bufio.NewScanner(in)
		scanner.Buffer(make([]byte, 64*1024), 16*1024*1024)
		for scanner.Scan() {
			select {
			case t.lines <- scanner.Text():
			case <-ctx.Done():
				return
			}
		}
		t.err = scanner.Err()
	}()
	return t
}

func (t *terminalInput) read(ctx context.Context, interrupts <-chan os.Signal) (string, error) {
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case <-interrupts:
		return "", ErrInterrupted
	case line, ok := <-t.lines:
		if ok {
			return line, nil
		}
		if t.err != nil {
			return "", t.err
		}
		return "", io.EOF
	}
}
