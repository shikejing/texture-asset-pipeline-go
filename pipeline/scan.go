package pipeline

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// assetFile is a discovered source file with its logical path.
type assetFile struct {
	logical string
	abs     string
}

// scanDir walks root in sorted order, returning regular files only.
// Traversal order is fully deterministic regardless of filesystem order:
// every directory's entries are sorted by name before descent.
func scanDir(root string) ([]assetFile, error) {
	var out []assetFile
	var walk func(dir string) error
	walk = func(dir string) error {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return err
		}
		sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
		for _, e := range entries {
			full := filepath.Join(dir, e.Name())
			if e.IsDir() {
				if err := walk(full); err != nil {
					return err
				}
				continue
			}
			info, err := e.Info()
			if err != nil {
				return err
			}
			if !info.Mode().IsRegular() {
				continue
			}
			rel, err := filepath.Rel(root, full)
			if err != nil {
				return err
			}
			out = append(out, assetFile{
				logical: filepath.ToSlash(rel),
				abs:     full,
			})
		}
		return nil
	}
	if err := walk(root); err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool { return out[i].logical < out[j].logical })
	return out, nil
}

// isSidecar reports whether a logical path is a reference/link sidecar.
func isSidecar(logical string) bool {
	return strings.HasSuffix(logical, sidecarSuffix)
}

// ownerPath maps "a/b.png.refs.json" to "a/b.png".
func ownerPath(sidecar string) string {
	return strings.TrimSuffix(sidecar, sidecarSuffix)
}

// sortedKeys is a small helper reused by deterministic assembly steps.
func sortedKeys(m map[string]struct{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
