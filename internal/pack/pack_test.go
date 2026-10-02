package pack

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func testManifest() *Manifest {
	return &Manifest{
		Magic:            string(Magic[:]),
		FormatVersion:    FormatVersion,
		MinReaderVersion: MinReaderVersion,
		CodecVersion:     1,
		Assets: []AssetEntry{
			{RelPath: "a.png", Kind: "texture", ContentHash: "ch1", BlobHash: "bh1", BlobSize: 3, Width: 4, Height: 4},
			{RelPath: "b.json", Kind: "descriptor", ContentHash: "ch2", BlobHash: "bh2", BlobSize: 4},
		},
		References: []Reference{
			{FromAsset: "b.json", Field: "tex", ToHash: "ch1", ToPath: "@ch1"},
		},
	}
}

func testBlobs() map[string][]byte {
	return map[string][]byte{
		"bh1": []byte("aaa"),
		"bh2": []byte("bbbb"),
	}
}

func TestBuildReadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	pkg := filepath.Join(dir, "out.pkg")
	w := NewWriter(pkg)
	m := testManifest()
	if err := w.Build(m, testBlobs()); err != nil {
		t.Fatal(err)
	}

	r := NewReader(pkg)
	got, err := r.ReadManifest()
	if err != nil {
		t.Fatal(err)
	}
	if got.FormatVersion != FormatVersion {
		t.Fatalf("format version = %d, want %d", got.FormatVersion, FormatVersion)
	}
	if got.AssetCount != 2 || got.BlobCount != 2 {
		t.Fatalf("counts = %d/%d, want 2/2", got.AssetCount, got.BlobCount)
	}
	if len(got.Assets) != 2 || len(got.Blobs) != 2 {
		t.Fatalf("assets/blobs len = %d/%d", len(got.Assets), len(got.Blobs))
	}
}

func TestReadBlob(t *testing.T) {
	dir := t.TempDir()
	pkg := filepath.Join(dir, "out.pkg")
	if err := NewWriter(pkg).Build(testManifest(), testBlobs()); err != nil {
		t.Fatal(err)
	}
	r := NewReader(pkg)
	for hash, want := range testBlobs() {
		got, err := r.ReadBlob(hash)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("blob %s = %q, want %q", hash, got, want)
		}
	}
	_, err := r.ReadBlob("nonexistent")
	if err == nil {
		t.Fatal("expected error for missing blob")
	}
}

func TestVerify(t *testing.T) {
	dir := t.TempDir()
	pkg := filepath.Join(dir, "out.pkg")
	if err := NewWriter(pkg).Build(testManifest(), testBlobs()); err != nil {
		t.Fatal(err)
	}
	if err := NewReader(pkg).Verify(); err != nil {
		t.Fatalf("verify: %v", err)
	}
}

func TestCorruptPackage(t *testing.T) {
	dir := t.TempDir()
	pkg := filepath.Join(dir, "out.pkg")
	if err := NewWriter(pkg).Build(testManifest(), testBlobs()); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(pkg)
	b[len(b)-1] ^= 0xFF
	_ = os.WriteFile(pkg, b, 0o644)
	if err := NewReader(pkg).Verify(); err != ErrCorrupt {
		t.Fatalf("expected ErrCorrupt, got %v", err)
	}
}

func TestDeterministicLayout(t *testing.T) {
	dir := t.TempDir()
	pkg1 := filepath.Join(dir, "a.pkg")
	pkg2 := filepath.Join(dir, "b.pkg")
	// Build with blobs in different map iteration orders (map is unordered,
	// but the writer sorts).
	blobs := testBlobs()
	if err := NewWriter(pkg1).Build(testManifest(), blobs); err != nil {
		t.Fatal(err)
	}
	if err := NewWriter(pkg2).Build(testManifest(), blobs); err != nil {
		t.Fatal(err)
	}
	b1, _ := os.ReadFile(pkg1)
	b2, _ := os.ReadFile(pkg2)
	if !bytes.Equal(b1, b2) {
		t.Fatal("packages differ across builds")
	}
}

func TestNoPartialPackageOnError(t *testing.T) {
	dir := t.TempDir()
	pkg := filepath.Join(dir, "out.pkg")
	// Build with a manifest that has no blobs (should still work).
	m := &Manifest{
		Magic:            string(Magic[:]),
		FormatVersion:    FormatVersion,
		MinReaderVersion: MinReaderVersion,
		CodecVersion:     1,
	}
	if err := NewWriter(pkg).Build(m, map[string][]byte{}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(pkg); err != nil {
		t.Fatalf("package not written: %v", err)
	}
}
