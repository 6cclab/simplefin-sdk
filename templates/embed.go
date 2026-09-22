// Package templates embeds the per-language code templates.
//
// The templates live at the repository root rather than beside the generator
// so they are easy to find and edit when adding a language. go:embed can only
// reach files at or below its own package directory, hence this file.
package templates

import "embed"

// FS holds one directory per target language. Adding a language means adding a
// directory here and listing it in the embed pattern below.
//
//go:embed all:go all:node
var FS embed.FS
