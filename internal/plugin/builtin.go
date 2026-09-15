package plugin

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"

	"github.com/chowyu12/aiclaw/internal/model"
)

// builtinUUIDNamespace keeps a bundled plugin's record identity stable across
// restarts and upgrades, the way embedded skills do.
const builtinUUIDNamespace = "aiclaw/builtin-plugin/"

// EnsureBuiltins syncs the plugins shipped inside the binary into the plugin
// directory and records them.
//
// Each bundle is written on every startup so an upgrade replaces its files,
// while the user's enable choice is preserved. Bundles stay disabled until the
// user enables one: shipping a capability is not the same as granting it.
//
// A bundle that declares another operating system is skipped rather than
// failing startup. Any other manifest error is a defect in a manifest we ship,
// so it is reported.
func (i *Installer) EnsureBuiltins(ctx context.Context, bundles fs.FS) error {
	entries, err := fs.ReadDir(bundles, ".")
	if err != nil {
		return fmt.Errorf("read bundled plugins: %w", err)
	}
	existing, err := i.store.ListPlugins(ctx)
	if err != nil {
		return err
	}
	enabled := make(map[string]bool, len(existing))
	for _, item := range existing {
		if item.Source == model.PluginSourceBuiltin {
			enabled[item.UUID] = item.Enabled
		}
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		if err := i.ensureBuiltin(ctx, bundles, entry.Name(), enabled); err != nil {
			return err
		}
	}
	return nil
}

func (i *Installer) ensureBuiltin(ctx context.Context, bundles fs.FS, name string, enabled map[string]bool) error {
	dest := filepath.Join(i.PluginsDir(), name)
	if err := syncTree(bundles, name, dest); err != nil {
		return fmt.Errorf("sync bundled plugin %s: %w", name, err)
	}
	resolved, err := Resolve(dest)
	if err != nil {
		if errors.Is(err, ErrUnsupportedHost) {
			// Leave nothing behind for a bundle this host cannot run.
			_ = os.RemoveAll(dest)
			return nil
		}
		return fmt.Errorf("bundled plugin %s: %w", name, err)
	}
	identifier := strings.TrimSpace(resolved.Manifest.ID)
	if identifier == "" {
		return fmt.Errorf("bundled plugin %s must declare a manifest id", name)
	}
	plugin := &model.Plugin{
		UUID:        uuid.NewSHA1(uuid.NameSpaceURL, []byte(builtinUUIDNamespace+identifier)).String(),
		PluginID:    identifier,
		Source:      model.PluginSourceBuiltin,
		Name:        resolved.Manifest.Name,
		Description: resolved.Manifest.Description,
		Version:     resolved.Manifest.Version,
		InstallDir:  dest,
		Manifest:    resolved.Raw,
	}
	plugin.Enabled = enabled[plugin.UUID]
	if err := i.store.UpsertPlugin(ctx, plugin); err != nil {
		return err
	}
	return i.persistContributions(ctx, plugin, resolved)
}

// syncTree writes an embedded bundle to disk, replacing whatever was there so
// an upgrade cannot leave a stale file behind.
func syncTree(bundles fs.FS, name, dest string) error {
	if err := os.RemoveAll(dest); err != nil {
		return err
	}
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return err
	}
	return fs.WalkDir(bundles, name, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(name, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dest, relative)
		if entry.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := fs.ReadFile(bundles, path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o644)
	})
}
