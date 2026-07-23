package web

import (
	"embed"
	"io/fs"
)

//go:embed dist
var assets embed.FS

// FS contains the built client with paths rooted at the distribution directory.
var FS fs.FS = mustSub(assets, "dist")

func mustSub(source fs.FS, directory string) fs.FS {
	sub, err := fs.Sub(source, directory)
	if err != nil {
		panic(err)
	}
	return sub
}
