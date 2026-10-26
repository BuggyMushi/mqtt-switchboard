package web

import "embed"

// Assets contains the embedded single-page dashboard HTML
//
//go:embed *
var Assets embed.FS
