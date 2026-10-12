// Package webassets embeds the AdminLTE templates and front-end assets so the
// admin panel ships as a single self-contained binary.
package webassets

import (
	"embed"
	"io/fs"
)

//go:embed templates static
var assets embed.FS

// TemplatesFS is the embedded template directory, rooted at "templates".
var TemplatesFS = mustSub("templates")

// StaticFS is the embedded static asset directory, rooted at "static".
var StaticFS = mustSub("static")

func mustSub(dir string) fs.FS {
	sub, err := fs.Sub(assets, dir)
	if err != nil {
		panic("webassets: " + err.Error())
	}
	return sub
}
