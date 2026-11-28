package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/protocol"
)

const sessionLinkPrefix = "aiclaw://sessions/"

func sessionTarget(target string) (string, error) {
	id := strings.TrimPrefix(target, sessionLinkPrefix)
	if id == "" || len(id) > 128 || strings.ContainsFunc(id, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-')
	}) {
		return "", errors.New("invalid AIClaw session ID or link")
	}
	return id, nil
}

type listedSession struct {
	ID, Title, Workdir, ParentID string
	ArchivedAt                   *string
}

func listSessions(ctx context.Context, c *connection, includeArchived bool) ([]listedSession, error) {
	var result struct{ Sessions []listedSession }
	if err := c.call(ctx, protocol.MethodSessionList, struct{}{}, &result); err != nil {
		return nil, err
	}
	if !includeArchived {
		return result.Sessions, nil
	}
	var archived struct{ Sessions []listedSession }
	if err := c.call(ctx, protocol.MethodSessionArchived, struct{}{}, &archived); err != nil {
		return nil, err
	}
	return append(result.Sessions, archived.Sessions...), nil
}

func runSessions(ctx context.Context, c *connection, o options, in io.Reader, out, stderr io.Writer, interrupts <-chan os.Signal) error {
	if !o.open && len(o.args) == 2 {
		switch o.args[0] {
		case "show":
			id, err := sessionTarget(o.args[1])
			if err != nil {
				return err
			}
			return printHistory(ctx, c, id, out)
		case "open":
			return openSessionHistory(ctx, c, o.args[1], out)
		}
	}
	var query string
	if len(o.args) > 1 && o.args[0] == "search" {
		query = strings.ToLower(strings.TrimSpace(strings.Join(o.args[1:], " ")))
	}
	if query == "" && (o.open || len(o.args) != 1 || o.args[0] != "list") {
		return errors.New("use aiclaw sessions list, sessions search QUERY [--open] or sessions open ID_OR_LINK")
	}
	rows, err := listSessions(ctx, c, query != "")
	if err != nil {
		return err
	}
	var matches []listedSession
	for _, row := range rows {
		if query != "" && !strings.Contains(strings.ToLower(row.Title), query) &&
			!strings.Contains(strings.ToLower(row.Workdir), query) && !strings.Contains(strings.ToLower(row.ID), query) {
			continue
		}
		matches = append(matches, row)
	}
	if o.open && len(matches) == 1 {
		return openSessionHistory(ctx, c, matches[0].ID, out)
	}
	for i, row := range matches {
		if o.open {
			fmt.Fprintf(out, "%d. ", i+1)
		}
		fmt.Fprintf(out, "%s\t%s\t%s", row.ID, terminalText(row.Title), terminalText(row.Workdir))
		if query != "" {
			fmt.Fprintf(out, "\t%s%s", sessionLinkPrefix, row.ID)
			if row.ArchivedAt != nil {
				fmt.Fprint(out, "\tarchived")
			}
		}
		fmt.Fprintln(out)
	}
	if !o.open {
		return nil
	}
	if len(matches) == 0 {
		return errors.New("no matching sessions")
	}
	fmt.Fprint(stderr, "Open result number (blank to cancel): ")
	inputCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	answer, err := newTerminalInput(inputCtx, in).read(ctx, interrupts)
	if errors.Is(err, io.EOF) || (err == nil && strings.TrimSpace(answer) == "") {
		return nil
	}
	if err != nil {
		return err
	}
	index, err := strconv.Atoi(strings.TrimSpace(answer))
	if err != nil || index < 1 || index > len(matches) {
		return errors.New("invalid search result number")
	}
	return openSessionHistory(ctx, c, matches[index-1].ID, out)
}

func openSessionHistory(ctx context.Context, c *connection, target string, out io.Writer) error {
	id, err := sessionTarget(target)
	if err != nil {
		return err
	}
	var history protocol.SessionHistoryResult
	if err := c.call(ctx, protocol.MethodSessionHistory, protocol.SessionIDParams{SessionID: id}, &history); err != nil {
		return err
	}
	rows, err := listSessions(ctx, c, true)
	if err != nil {
		return err
	}
	var parent string
	for _, row := range rows {
		if row.ID == id {
			parent = row.ParentID
			break
		}
	}
	writeHistory(out, history.Items)
	linked := map[string]bool{id: true}
	showLink := func(title, candidate string) {
		if normalized, invalid := sessionTarget(candidate); invalid != nil || normalized != candidate || linked[candidate] {
			return
		}
		linked[candidate] = true
		fmt.Fprintf(out, "%s: %s%s\n", terminalText(title), sessionLinkPrefix, candidate)
	}
	showLink("Parent session", parent)
	for _, row := range rows {
		if row.ParentID == id {
			showLink("Child agent "+row.Title, row.ID)
		}
	}
	for _, item := range history.Items {
		for _, ref := range item.References {
			showLink("Referenced session "+ref.Title, ref.ID)
		}
	}
	return nil
}
