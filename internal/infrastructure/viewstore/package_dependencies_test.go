package viewstore_test

import (
	"go/build"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

const modulePath = "keyloop-coding-challenge/"

// TestViewstorePackageDependencies pins the layering inside viewstore: the root composes
// repositories (and the migrations facade); repositories may use models and the
// application models they map to, never the root; models and migrations stand alone.
func TestViewstorePackageDependencies(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	root := filepath.Dir(file)
	allowed := map[string][]string{
		"models":     nil,
		"migrations": nil,
		"repositories": {
			"internal/infrastructure/viewstore/models",
			"internal/application/projection",
			"internal/application/query",
			"internal/domain",
		},
	}
	for pkg, may := range allowed {
		p, err := build.ImportDir(filepath.Join(root, pkg), 0)
		if err != nil {
			t.Fatalf("%s: %v", pkg, err)
		}
		for _, imp := range p.Imports {
			if !strings.HasPrefix(imp, modulePath) {
				continue
			}
			if !contains(may, strings.TrimPrefix(imp, modulePath)) {
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
