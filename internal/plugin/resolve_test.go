package plugin

import (
	"path/filepath"
	"strings"
	"testing"
)

func writeBundle(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for name, content := range files {
		path := filepath.Join(root, name)
		mustMkdirAll(t, filepath.Dir(path))
		mustWriteFile(t, path, content)
	}
	return root
}

func TestResolveFallsBackToDirectoryProbing(t *testing.T) {
	root := writeBundle(t, map[string]string{
		".codex-plugin/plugin.json":     `{"name":"Research Kit","version":"1.2.0"}`,
		"skills/research/manifest.json": `{"name":"Research","main":"main.py","permissions":["process.execute"],"tools":[{"name":"research_echo"}]}`,
		"skills/research/main.py":       "print('ok')",
		"mcp.json":                      `{"mcpServers":{"echo":{"command":"/bin/echo","args":["ready"]}}}`,
	})

	resolved, err := Resolve(root)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Manifest.Name != "Research Kit" || resolved.Manifest.Version != "1.2.0" {
		t.Fatalf("manifest not read: %+v", resolved.Manifest)
	}
	if len(resolved.Skills) != 1 || resolved.Skills[0].RelDir != filepath.Join("skills", "research") {
		t.Fatalf("skill contribution not probed: %+v", resolved.Skills)
	}
	if len(resolved.Servers) != 1 || resolved.Servers["echo"].Command != "/bin/echo" {
		t.Fatalf("mcp contribution not probed: %+v", resolved.Servers)
	}
	// A legacy manifest cannot have declared permissions, so they are derived
	// from what the contributions actually need.
	if got := strings.Join(resolved.Permissions, ","); got != "process.execute" {
		t.Fatalf("derived permissions = %q", got)
	}
}

func TestResolveWithoutManifestNamesBundleAfterDirectory(t *testing.T) {
	root := writeBundle(t, map[string]string{"skills/a/SKILL.md": "---\nname: A\n---\nbody"})
	resolved, err := Resolve(root)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Manifest.Name != filepath.Base(root) {
		t.Fatalf("synthetic manifest name = %q", resolved.Manifest.Name)
	}
	if len(resolved.Skills) != 1 {
		t.Fatalf("skills = %+v", resolved.Skills)
	}
}

func TestResolvePrefersDeclaredContributions(t *testing.T) {
	root := writeBundle(t, map[string]string{
		".aiclaw-plugin/plugin.json": `{"schema_version":1,"id":"acme.kit","name":"Kit","permissions":["network.access"],
			"contributes":{"skills":[{"dir":"bundled/helper"}],"mcpServers":{"remote":{"url":"https://example.invalid/mcp"}}}}`,
		"bundled/helper/SKILL.md": "---\nname: Helper\n---\nhelp",
		// Probed locations must be ignored once the manifest declares its own.
		"skills/ignored/SKILL.md": "---\nname: Ignored\n---\nno",
		"mcp.json":                `{"mcpServers":{"ignored":{"command":"/bin/false"}}}`,
	})
	resolved, err := Resolve(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(resolved.Skills) != 1 || resolved.Skills[0].Info.Name != "Helper" {
		t.Fatalf("declared skills were not preferred: %+v", resolved.Skills)
	}
	if len(resolved.Servers) != 1 {
		t.Fatalf("declared servers were not preferred: %+v", resolved.Servers)
	}
	if _, ok := resolved.Servers["remote"]; !ok {
		t.Fatalf("declared server missing: %+v", resolved.Servers)
	}
}

func TestResolveRejectsUndeclaredPermissionUnderContract(t *testing.T) {
	root := writeBundle(t, map[string]string{
		".aiclaw-plugin/plugin.json": `{"schema_version":1,"id":"acme.kit","name":"Kit",
			"contributes":{"mcpServers":{"local":{"command":"/bin/echo"}}}}`,
	})
	_, err := Resolve(root)
	if err == nil || !strings.Contains(err.Error(), "process.execute") {
		t.Fatalf("undeclared permission was accepted: %v", err)
	}
}

func TestResolveRejectsUnknownAndExternalProviders(t *testing.T) {
	cases := map[string]string{
		"unknown builtin provider": `{"schema_version":1,"name":"Kit","permissions":["computer.control","filesystem.write"],
			"contributes":{"tools":[{"provider":"builtin:nope","names":["x"]}]}}`,
		"third-party native tool": `{"schema_version":1,"name":"Kit",
			"contributes":{"tools":[{"provider":"acme/native","names":["x"]}]}}`,
		"channel without provider": `{"schema_version":1,"name":"Kit",
			"contributes":{"channels":[{"id":"c","provider":"acme/chan"}]}}`,
	}
	for name, manifest := range cases {
		t.Run(name, func(t *testing.T) {
			root := writeBundle(t, map[string]string{".aiclaw-plugin/plugin.json": manifest})
			if _, err := Resolve(root); err == nil {
				t.Fatal("invalid native contribution was accepted")
			}
		})
	}
}

func TestResolveAcceptsRegisteredBuiltinProvider(t *testing.T) {
	root := writeBundle(t, map[string]string{
		".aiclaw-plugin/plugin.json": `{"schema_version":1,"id":"aiclaw.computer-use","name":"Computer Use",
			"permissions":["computer.control","filesystem.write"],
			"contributes":{"tools":[{"provider":"builtin:computer_use","names":["computer"]}]}}`,
	})
	resolved, err := Resolve(root)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(resolved.Permissions, ","); got != "computer.control,filesystem.write" {
		t.Fatalf("permissions = %q", got)
	}
	// The provider is declared, so the bundle installs. Whether this build
	// carries an implementation is a separate question answered by Runtime.
	if _, ok := LookupProvider("builtin:computer_use"); !ok {
		t.Fatal("builtin:computer_use is not declared")
	}
}

func TestResolveRejectsEscapingSkillDirAndNewerSchema(t *testing.T) {
	escaping := writeBundle(t, map[string]string{
		".aiclaw-plugin/plugin.json": `{"schema_version":1,"name":"Kit","contributes":{"skills":[{"dir":"../outside"}]}}`,
	})
	if _, err := Resolve(escaping); err == nil || !strings.Contains(err.Error(), "escapes") {
		t.Fatalf("escaping skill dir was accepted: %v", err)
	}
	newer := writeBundle(t, map[string]string{
		".aiclaw-plugin/plugin.json": `{"schema_version":99,"name":"Kit"}`,
	})
	if _, err := Resolve(newer); err == nil || !strings.Contains(err.Error(), "schema version") {
		t.Fatalf("newer schema version was accepted: %v", err)
	}
}

func TestResolveRejectsExecutableSkillWithoutPermission(t *testing.T) {
	root := writeBundle(t, map[string]string{
		"plugin.json":                 `{"name":"Broken"}`,
		"skills/broken/manifest.json": `{"name":"Broken","main":"main.py","tools":[{"name":"broken_tool"}]}`,
		"skills/broken/main.py":       "print('no permission')",
	})
	if _, err := Resolve(root); err == nil {
		t.Fatal("executable skill without process.execute was accepted")
	}
}
