package admin

import (
	"io/fs"
)

// sourcesFS merges the two embedded trees under one root ("templates/..." and "static/...").
type mergedFS struct{}

func sourcesFS() fs.FS { return mergedFS{} }

func (mergedFS) Open(name string) (fs.File, error) {
	if len(name) >= 10 && name[:10] == "templates/" {
		return templatesFS.Open(name)
	}
	return staticFS.Open(name)
}
