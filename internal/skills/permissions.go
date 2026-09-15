package skills

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/chowyu12/aiclaw/internal/model"
)

const (
	PermissionProcessExecute  = "process.execute"
	PermissionFilesystemRead  = "filesystem.read"
	PermissionFilesystemWrite = "filesystem.write"
	PermissionNetworkAccess   = "network.access"

	// These permissions are only meaningful for plugin bundles, but the
	// enumeration is shared so a plugin and the skills it contributes are
	// normalized and displayed through one code path.
	PermissionComputerControl = "computer.control"
	PermissionChannelReceive  = "channel.receive"
	PermissionChannelSend     = "channel.send"
	PermissionSecretsRead     = "secrets.read"
)

var supportedPermissions = map[string]bool{
	PermissionProcessExecute: true, PermissionFilesystemRead: true,
	PermissionFilesystemWrite: true, PermissionNetworkAccess: true,
	PermissionComputerControl: true, PermissionChannelReceive: true,
	PermissionChannelSend: true, PermissionSecretsRead: true,
}

func NormalizePermissions(values []string) ([]string, error) {
	aliases := map[string]string{
		"process": PermissionProcessExecute, "exec": PermissionProcessExecute,
		"filesystem:read": PermissionFilesystemRead, "filesystem:write": PermissionFilesystemWrite,
		"network": PermissionNetworkAccess, "computer": PermissionComputerControl,
		"channel:receive": PermissionChannelReceive, "channel:send": PermissionChannelSend,
		"secrets": PermissionSecretsRead,
	}
	result := make([]string, 0, len(values))
	seen := make(map[string]bool, len(values))
	for _, value := range values {
		value = strings.ToLower(strings.TrimSpace(value))
		if alias := aliases[value]; alias != "" {
			value = alias
		}
		if !supportedPermissions[value] {
			return nil, fmt.Errorf("unsupported skill permission %q", value)
		}
		if !seen[value] {
			result = append(result, value)
			seen[value] = true
		}
	}
	return result, nil
}

func StoredPermissions(value model.JSON) ([]string, error) {
	if len(value) == 0 {
		return nil, nil
	}
	var permissions []string
	if err := json.Unmarshal(value, &permissions); err != nil {
		return nil, fmt.Errorf("parse skill permissions: %w", err)
	}
	return NormalizePermissions(permissions)
}

func ValidateExecutable(skill model.Skill) ([]string, error) {
	permissions, err := StoredPermissions(skill.Permissions)
	if err != nil {
		return nil, err
	}
	if skill.MainFile == "" {
		return permissions, nil
	}
	for _, permission := range permissions {
		if permission == PermissionProcessExecute {
			return permissions, nil
		}
	}
	return nil, fmt.Errorf("executable skill %q requires %q permission", skill.Name, PermissionProcessExecute)
}
