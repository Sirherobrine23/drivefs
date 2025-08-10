package drivefs

import (
	"iter"
	"path"
	"strings"

	"sirherobrine23.com.br/Sirherobrine23/drivefs/internal/slice"
)

type pathSplit [2]string

func (p pathSplit) Path() string { return p[0] }
func (p pathSplit) Name() string { return p[1] }

type pathManipulate string

// Clean path and fix slash's
func (p pathManipulate) CleanPath() string {
	n := strings.Trim(path.Clean(strings.ReplaceAll(string(p), "\\", "/")), "/")
	if n == "" || n == "." {
		n = "/"
	}
	return n
}

// convert all '/' to "%2f"
func (p pathManipulate) EscapeName() string {
	return strings.Join(strings.Split(string(p), "/"), "%%2f")
}

// Check if path is folder
func (p pathManipulate) IsSubFolder() bool { return len(p.SplitPath()) >= 2 }

// Check if path is '.' or '/'
func (p pathManipulate) IsRoot() bool {
	switch p.CleanPath() {
	case ".", "/":
		return true
	default:
		return false
	}
}

// Return slice with this [][path(string), filename(string)]
func (p pathManipulate) SplitPath() slice.Slice[pathSplit] {
	nodes := slice.Slice[pathSplit]{}
	for path, name := range p.SplitPathSeq() {
		nodes.Push(pathSplit{path, name})
	}
	return nodes
}

// Return iter.Seq2[path(string), filename(string)]
func (p pathManipulate) SplitPathSeq() iter.Seq2[string, string] {
	return func(yield func(path string, name string) bool) {
		name := p.CleanPath()
		if name == "/" {
			yield("/", "/")
			return
		}

		lastNode := "/"
		for name := range strings.SplitSeq(name, "/") {
			lastNode = path.Join(lastNode, strings.ReplaceAll(name, "%%2f", "/"))
			if !yield(lastNode, name) {
				return
			}
		}
	}
}
