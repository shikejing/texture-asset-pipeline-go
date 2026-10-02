package scan

import (
	"os"
	"path/filepath"
	"testing"
)

func TestScan(t *testing.T) {
	dir := t.TempDir()
	// Create files.
	for _, f := range []string{"a.png", "b.jpg", "c.json", "d.txt", "sub/e.png"} {
		p := filepath.Join(dir, f)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	s := New(dir)
	assets, err := s.Scan()
	if err != nil {
		t.Fatal(err)
	}
	// Should find a.png, b.jpg, c.json, sub/e.png (not d.txt).
	if len(assets) != 4 {
		t.Fatalf("expected 4 assets, got %d", len(assets))
	}
	// Verify sorted order.
	for i := 1; i < len(assets); i++ {
		if assets[i-1].RelPath >= assets[i].RelPath {
			t.Fatalf("assets not sorted: %s >= %s", assets[i-1].RelPath, assets[i].RelPath)
		}
	}
	// Verify kinds.
	kinds := map[string]bool{}
	for _, a := range assets {
		kinds[string(a.Kind)] = true
	}
	if !kinds["texture"] || !kinds["descriptor"] {
		t.Fatalf("missing kinds: %v", kinds)
	}
}

func TestScanEmpty(t *testing.T) {
	dir := t.TempDir()
	assets, err := New(dir).Scan()
	if err != nil {
		t.Fatal(err)
	}
	if len(assets) != 0 {
		t.Fatalf("expected 0 assets, got %d", len(assets))
	}
}

func TestHashFile(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "f.bin")
	if err := os.WriteFile(p, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	h, err := HashFile(p)
	if err != nil {
		t.Fatal(err)
	}
	// sha256("hello") = 2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824
	if h != "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824" {
		t.Fatalf("hash = %s", h)
	}
}
