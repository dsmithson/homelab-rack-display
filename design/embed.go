// Package design embeds the web assets (screens, tokens, runtime, fonts) so
// the rack-display binary is self-contained.
package design

import "embed"

//go:embed index.html tokens.css display.js fonts screens
var FS embed.FS
