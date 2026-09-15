// Package bundled carries the manifests of the plugins shipped inside the
// binary. Each top-level directory is one bundle, synced to disk and recorded
// on startup, always disabled until the user enables it.
package bundled

import (
	"embed"
	"io/fs"
)

//go:embed computer-use wecom wechat
var bundles embed.FS

// FS returns the bundled plugin tree.
func FS() fs.FS { return bundles }
