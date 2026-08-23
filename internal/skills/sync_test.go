package skills

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/chowyu12/aiclaw/internal/model"
)

type builtinMemoryStore struct{ items map[string]model.Skill }

func (s *builtinMemoryStore) ListSkills(context.Context) ([]model.Skill, error) {
	items := make([]model.Skill, 0, len(s.items))
	for _, item := range s.items {
		items = append(items, item)
	}
	return items, nil
}

func (s *builtinMemoryStore) UpsertSkill(_ context.Context, skill *model.Skill) error {
	if s.items == nil {
		s.items = make(map[string]model.Skill)
	}
	s.items[skill.UUID] = *skill
	return nil
}

func TestEnsureBuiltinsUsesStableIDsAndPreservesDisabledState(t *testing.T) {
	store := &builtinMemoryStore{}
	root := t.TempDir()
	if err := EnsureBuiltins(context.Background(), store, root); err != nil {
		t.Fatal(err)
	}
	if len(store.items) != len(BuiltinSkills()) {
		t.Fatalf("registered %d builtins, want %d", len(store.items), len(BuiltinSkills()))
	}
	var disabledID string
	for id, item := range store.items {
		disabledID = id
		item.Enabled = false
		store.items[id] = item
		if item.InstallDir == "" || filepath.Dir(item.InstallDir) != filepath.Join(root, "skills") {
			t.Fatalf("unexpected install dir: %q", item.InstallDir)
		}
		break
	}
	if err := EnsureBuiltins(context.Background(), store, root); err != nil {
		t.Fatal(err)
	}
	if store.items[disabledID].Enabled {
		t.Fatal("builtin enable state was overwritten during resync")
	}
}
