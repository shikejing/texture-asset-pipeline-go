// Package scan discovers source assets under a root directory.
package scan

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/shikejing/texture-asset-pipeline-go/internal/asset"
)

// textureExts are recognized texture source extensions.
var textureExts = map[string]bool{
	".png": true,
	".jpg": true,
	".jpeg": true,
}

// descriptorExts are recognized descriptor source extensions.
var descriptorExts = map[string]bool{
	".json": true,
}

// Scanner walks a source root and collects assets.
type Scanner struct {
	Root string
}

// New returns a Scanner rooted at root.
func New(root string) *Scanner {
	return &Scanner{Root: root}
}

// Scan walks the root and returns all recognized assets in deterministic
// order (sorted by relative path).
func (s *Scanner) Scan() ([]asset.Asset, error) {
	var assets []asset.Asset
	err := filepath.WalkDir(s.Root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, rerr := filepath.Rel(s.Root, path)
		if rerr != nil {
			return rerr
		}
		rel = filepath.ToSlash(rel)
		ext := strings.ToLower(filepath.Ext(rel))
		kind := asset.KindUnknown
		switch {
		case textureExts[ext]:
			kind = asset.KindTexture
		case descriptorExts[ext]:
			kind = asset.KindDescriptor
		default:
			return nil
		}
		info, ierr := d.Info()
		if ierr != nil {
			return ierr
		}
		assets = append(assets, asset.Asset{
			RelPath: rel,
			AbsPath: path,
			Size:    info.Size(),
			ModTime: info.ModTime(),
			Kind:    kind,
		})
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(assets, func(i, j int) bool { return assets[i].RelPath < assets[j].RelPath })
	return assets, nil
}

// HashFile computes the sha256 of a file's contents.
func HashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
