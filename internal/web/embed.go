// Package web embeds the built Svelte SPA for serving from the Go binary.
package web

import "embed"

//go:embed all:dist
var DistFS embed.FS
