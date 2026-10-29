package presentation_test

import (
	"go/build"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

const presentationPath = "keyloop-coding-challenge/internal/presentation"

// TestPresentationPackageDependencies pins the dependency direction: the root composes
// commands, queries and probes; those may share httpkit but not import the root or
// each other; httpkit imports no presentation package. Go only rejects cycles, so a
// sideways import (queries → commands) would otherwise go unnoticed.
func TestPresentationPackageDependencies(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	root := filepath.Dir(file)
	allowed := map[string][]string{
		"httpkit":  nil,
		"commands": {"httpkit"},
		"queries":  {"httpkit"},
		"probes":   {"httpkit"},
		"apidocs":  {"httpkit"},
	}
	for pkg, may := range allowed {
		p, err := build.ImportDir(filepath.Join(root, pkg), 0)
		if err != nil {
			t.Fatalf("%s: %v", pkg, err)
		}
		for _, imp := range append(p.Imports, p.TestImports...) {
			if imp != presentationPath && !strings.HasPrefix(imp, presentationPath+"/") {
				continue
			}
			if !contains(may, strings.TrimPrefix(imp, presentationPath+"/")) {
				t.Errorf("%s imports %s; it may import only %v", pkg, imp, may)
			}
		}
	}
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
