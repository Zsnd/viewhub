package architecture

import (
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// App-local tests must also stay independent; cross-app tests live separately
// in tests/integration. No UI dependency folders are traversed here.
func TestApplicationBoundaries(t *testing.T) {
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Clean(filepath.Join(dir, "../.."))
	for _, app := range []string{"platform", "capture"} {
		err := filepath.WalkDir(filepath.Join(root, "apps", app), func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() && (entry.Name() == "web" || entry.Name() == "dist") {
				return filepath.SkipDir
			}
			if entry.IsDir() || !strings.HasSuffix(path, ".go") {
				return nil
			}
			file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
			if err != nil {
				return err
			}
			for _, spec := range file.Imports {
				dependency, err := strconv.Unquote(spec.Path.Value)
				if err != nil {
					return err
				}
				if strings.HasPrefix(dependency, "viewhub/") && dependency != "viewhub/apps/"+app && !strings.HasPrefix(dependency, "viewhub/apps/"+app+"/") {
					t.Errorf("%s imports another application: %s; move joint tests to tests/integration", path, dependency)
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}
