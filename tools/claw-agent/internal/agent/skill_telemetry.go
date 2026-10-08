package agent

import (
	"context"
	"path/filepath"
	"strings"
	"sync"
	"unicode"

	"github.com/chowyu12/aiclaw/internal/telemetry"
	"go.opentelemetry.io/otel/log"
)

func WithSkillLogger(logger log.Logger) Option {
	return func(s *Session) { s.skillLogger = logger }
}

type skillUsageKey struct{}

// One tracker per executing turn; no history or tool payload is kept or exported.
type skillUsage struct {
	session  *Session
	turnID   string
	paths    map[string]string
	mu       sync.Mutex
	explicit map[string]bool
	emitted  map[string]bool
}

func (s *Session) newSkillUsage(turnID string) *skillUsage {
	if s.skillLogger == nil {
		return nil
	}
	u := &skillUsage{session: s, turnID: turnID, paths: map[string]string{}, explicit: map[string]bool{}, emitted: map[string]bool{}}
	for _, skill := range s.skills {
		path, err := filepath.Abs(filepath.Join(skill.Dir, "SKILL.md"))
		if err != nil {
			continue
		}
		if real, err := filepath.EvalSymlinks(path); err == nil {
			path = real
		}
		u.paths[path] = skill.Name
	}
	return u
}

func (u *skillUsage) markExplicit(text string) {
	if u == nil {
		return
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	for _, skill := range u.session.skills {
		needle := "$" + skill.Name
		for offset := 0; offset < len(text); {
			index := strings.Index(text[offset:], needle)
			if index < 0 {
				break
			}
			index += offset
			before, after := text[:index], text[index+len(needle):]
			// Avoid matching $foo inside $foobar, identifiers or escaped literals.
			left, right := []rune(before), []rune(after)
			if (len(left) == 0 || mentionBoundary(left[len(left)-1])) && (len(right) == 0 || mentionBoundary(right[0])) {
				u.explicit[skill.Name] = true
				break
			}
			offset = index + len(needle)
		}
	}
}

func mentionBoundary(r rune) bool {
	return unicode.IsSpace(r) || (unicode.IsPunct(r) && !strings.ContainsRune("_-\\", r))
}

func (u *skillUsage) loaded(_ context.Context, name string) {
	u.mu.Lock()
	invocation := "implicit"
	if u.explicit[name] {
		invocation = "explicit"
	}
	u.mu.Unlock()
	u.emit(name, invocation)
}

func (u *skillUsage) read(_ context.Context, path string) {
	if name, found := u.paths[path]; found {
		u.emit(name, "implicit")
	}
}

func (u *skillUsage) emit(name, invocation string) {
	key := invocation + ":" + name
	u.mu.Lock()
	if u.emitted[key] {
		u.mu.Unlock()
		return
	}
	u.emitted[key] = true
	u.mu.Unlock()
	telemetry.EmitSkill(u.session.skillLogger, u.session.ID, u.turnID, u.session.Model().Model, name, invocation)
}
