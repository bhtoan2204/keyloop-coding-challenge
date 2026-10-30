package application_test

import (
	"go/build"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestApplicationOwnsItsPorts pins the dependency direction of the application layer:
// it declares the ports (command.VehicleStore, query.ReadModel, projection.Store) and
// never imports their implementations, an ORM, a database driver or HTTP.
func TestApplicationOwnsItsPorts(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	root := filepath.Dir(file)
	forbidden := []string{
		"keyloop-coding-challenge/internal/infrastructure",
		"keyloop-coding-challenge/internal/presentation",
		"keyloop-coding-challenge/cmd",
		"gorm.io/",
		"github.com/jackc/",
		"database/sql",
		"net/http",
	}
	for _, pkg := range []string{"command", "query", "projection"} {
		p, err := build.ImportDir(filepath.Join(root, pkg), 0)
		if err != nil {
			t.Fatalf("%s: %v", pkg, err)
		}
		for _, imp := range p.Imports {
			for _, prefix := range forbidden {
				if imp == prefix || strings.HasPrefix(imp, prefix) {
					t.Errorf("application/%s imports %s", pkg, imp)
				}
			}
		}
	}
}
