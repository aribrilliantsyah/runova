// Package web embeds the panel: plain HTML, CSS and JavaScript with its vendored assets.
package web

import "embed"

// FS holds the panel files with index.html at the root.
//
//go:embed index.html css js img vendor
var FS embed.FS
