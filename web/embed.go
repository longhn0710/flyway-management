package web

import (
	"embed"
	"io/fs"
)

// Files contains the production frontend build.
//
//go:embed dist
var Files embed.FS

func Dist() (fs.FS, error) { return fs.Sub(Files, "dist") }
