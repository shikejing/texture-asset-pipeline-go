package pipeline_test

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"crypto/sha256"
	"os"
	"path/filepath"
	"testing"

	"github.com/shikejing/texture-asset-pipeline-go/internal/config"
	"github.com/shikejing/texture-asset-pipeline-go/internal/pack"
	"github.com/shikejing/texture-asset-pipeline-go/internal/pipeline"
)

// --- helpers ---

func makePNG(t *testing.T, path string, w, h, seed int) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.NRGBA{
				uint8((x*seed + y) % 256),
				uint8((y*seed + x) % 256),
				uint8((x + y + seed) % 256),
				255,
			})
		}
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func setupSource(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	makePNG(t, filepath.Join(dir, "textures", "wood.png"), 16, 16, 1)
	makePNG(t, filepath.Join(dir, "textures", "metal.png"), 12, 12, 2)
	writeFile(t, filepath.Join(dir, "material.json"), `{
  "type": "material",
  "textures": {
    "diffuse": "textures/wood.png",
    "metalness": "textures/metal.png"
  }
}`)
	return dir
}

func newPipeline(t *testing.T, src, out, cache string, workers int) *pipeline.Pipeline {
	t.Helper()
	cfg := config.Default()
	cfg.Source = src
	cfg.Output = out
	cfg.CacheDir = cache
	cfg.Workers = workers
	p, err := pipeline.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func build(t *testing.T, p *pipeline.Pipeline) *pipeline.Report {
	t.Helper()
	report, err := p.Build(context.Background())
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	return report
}

func readPkg(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// --- tests ---

func TestDeterminism(t *testing.T) {
	src := setupSource(t)
	cache := t.TempDir()
	out1 := filepath.Join(t.TempDir(), "a.pkg")
	out2 := filepath.Join(t.TempDir(), "b.pkg")

	p1 := newPipeline(t, src, out1, cache, 4)
	build(t, p1)
	p2 := newPipeline(t, src, out2, cache, 4)
	build(t, p2)

	b1 := readPkg(t, out1)
	b2 := readPkg(t, out2)
	if !bytes.Equal(b1, b2) {
		t.Fatal("rebuild produced non-identical packages")
	}
}

func TestIncrementalEqualsFull(t *testing.T) {
	src := setupSource(t)

	// Full rebuild (cold cache).
	coldCache := t.TempDir()
	coldOut := filepath.Join(t.TempDir(), "cold.pkg")
	pCold := newPipeline(t, src, coldOut, coldCache, 4)
	rCold := build(t, pCold)
	if rCold.Hits != 0 {
		t.Fatalf("cold build should have 0 hits, got %d", rCold.Hits)
	}

	// Incremental (warm cache).
	warmCache := t.TempDir()
	warmOut := filepath.Join(t.TempDir(), "warm.pkg")
	pWarm := newPipeline(t, src, warmOut, warmCache, 4)
	// Seed the warm cache from the cold build's cache.
	if err := copyDir(coldCache, warmCache); err != nil {
		t.Fatal(err)
	}
	rWarm := build(t, pWarm)
	if rWarm.Misses != 0 {
		t.Fatalf("warm build should have 0 misses, got %d", rWarm.Misses)
	}

	bCold := readPkg(t, coldOut)
	bWarm := readPkg(t, warmOut)
	if !bytes.Equal(bCold, bWarm) {
		t.Fatal("incremental output differs from full rebuild")
	}
}

func TestDedup(t *testing.T) {
	dir := t.TempDir()
	makePNG(t, filepath.Join(dir, "a", "tex.png"), 8, 8, 1)
	// Copy identical content under a different name/path.
	data, err := os.ReadFile(filepath.Join(dir, "a", "tex.png"))
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, "b", "tex_copy.png"), string(data))

	cache := t.TempDir()
	out := filepath.Join(t.TempDir(), "out.pkg")
	p := newPipeline(t, dir, out, cache, 4)
	r := build(t, p)

	if r.Total != 2 {
		t.Fatalf("expected 2 assets, got %d", r.Total)
	}
	// Both textures have identical content, so they share one blob.
	m, err := pack.NewReader(out).ReadManifest()
	if err != nil {
		t.Fatal(err)
	}
	if m.BlobCount != 1 {
		t.Fatalf("expected 1 deduped blob, got %d", m.BlobCount)
	}
	// Both assets point to the same blob hash.
	if m.Assets[0].BlobHash != m.Assets[1].BlobHash {
		t.Fatal("identical assets should reference the same blob")
	}
}

func TestWorkerCountDeterminism(t *testing.T) {
	src := setupSource(t)
	cache := t.TempDir()
	out1 := filepath.Join(t.TempDir(), "w1.pkg")
	out8 := filepath.Join(t.TempDir(), "w8.pkg")

	build(t, newPipeline(t, src, out1, cache, 1))
	build(t, newPipeline(t, src, out8, cache, 8))

	if !bytes.Equal(readPkg(t, out1), readPkg(t, out8)) {
		t.Fatal("packages differ across worker counts")
	}
}

func TestCacheCorruptionRecovery(t *testing.T) {
	src := setupSource(t)
	cache := t.TempDir()
	out1 := filepath.Join(t.TempDir(), "good.pkg")
	build(t, newPipeline(t, src, out1, cache, 4))

	// Corrupt one cache entry.
	entries, err := os.ReadDir(cache)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".cache" {
			p := filepath.Join(cache, e.Name())
			b, _ := os.ReadFile(p)
			b = append(b, 0xFF, 0xFF)
			_ = os.WriteFile(p, b, 0o644)
			break
		}
	}

	out2 := filepath.Join(t.TempDir(), "recovered.pkg")
	r := build(t, newPipeline(t, src, out2, cache, 4))
	if r.Errors != 0 {
		t.Fatalf("expected 0 errors after corruption, got %d", r.Errors)
	}
	if !bytes.Equal(readPkg(t, out1), readPkg(t, out2)) {
		t.Fatal("recovered package differs from original")
	}
}

func TestBadInputRecovery(t *testing.T) {
	dir := t.TempDir()
	makePNG(t, filepath.Join(dir, "good.png"), 8, 8, 1)
	writeFile(t, filepath.Join(dir, "broken.png"), "this is not a png")

	cache := t.TempDir()
	out := filepath.Join(t.TempDir(), "bad.pkg")
	cfg := config.Default()
	cfg.Source = dir
	cfg.Output = out
	cfg.CacheDir = cache
	p, err := pipeline.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	report, err := p.Build(context.Background())
	if err == nil {
		t.Fatal("expected build error for bad input")
	}
	if report == nil || report.Errors != 1 {
		t.Fatalf("expected 1 error, got %v", report)
	}
	// Package must not be written.
	if _, err := os.Stat(out); !os.IsNotExist(err) {
		t.Fatal("package should not exist after failed build")
	}
}

func TestVersionCompatibility(t *testing.T) {
	src := setupSource(t)
	cache := t.TempDir()
	out := filepath.Join(t.TempDir(), "v.pkg")
	build(t, newPipeline(t, src, out, cache, 4))

	// Valid package reads fine.
	r := pack.NewReader(out)
	if err := r.Verify(); err != nil {
		t.Fatalf("verify: %v", err)
	}
	m, err := r.ReadManifest()
	if err != nil {
		t.Fatal(err)
	}
	if m.FormatVersion != pack.FormatVersion {
		t.Fatalf("format version = %d, want %d", m.FormatVersion, pack.FormatVersion)
	}

	// Craft a too-old package.
	oldPkg := filepath.Join(t.TempDir(), "old.pkg")
	oldBytes := craftPackageWithVersion(t, out, 0, 1)
	_ = os.WriteFile(oldPkg, oldBytes, 0o644)
	if err := pack.NewReader(oldPkg).Verify(); err != pack.ErrTooOld {
		t.Fatalf("expected ErrTooOld, got %v", err)
	}

	// Craft a too-new package.
	newPkg := filepath.Join(t.TempDir(), "new.pkg")
	newBytes := craftPackageWithVersion(t, out, 99, 1)
	_ = os.WriteFile(newPkg, newBytes, 0o644)
	if err := pack.NewReader(newPkg).Verify(); err != pack.ErrTooNew {
		t.Fatalf("expected ErrTooNew, got %v", err)
	}
}

func TestConfigInvalidation(t *testing.T) {
	src := setupSource(t)
	cache := t.TempDir()
	out1 := filepath.Join(t.TempDir(), "a.pkg")
	cfg1 := config.Default()
	cfg1.Source = src
	cfg1.Output = out1
	cfg1.CacheDir = cache
	cfg1.CompressionLevel = 6
	p1, _ := pipeline.New(cfg1)
	r1 := build(t, p1)
	if r1.Misses != 3 {
		t.Fatalf("first build should miss all, got %d", r1.Misses)
	}

	// Change compression level → cache should be invalidated.
	out2 := filepath.Join(t.TempDir(), "b.pkg")
	cfg2 := cfg1
	cfg2.Output = out2
	cfg2.CompressionLevel = 9
	p2, _ := pipeline.New(cfg2)
	r2 := build(t, p2)
	if r2.Hits != 0 {
		t.Fatalf("config change should invalidate cache, got %d hits", r2.Hits)
	}
}

func TestReferenceRewrite(t *testing.T) {
	src := setupSource(t)
	cache := t.TempDir()
	out := filepath.Join(t.TempDir(), "ref.pkg")
	build(t, newPipeline(t, src, out, cache, 4))

	m, err := pack.NewReader(out).ReadManifest()
	if err != nil {
		t.Fatal(err)
	}
	if len(m.References) != 2 {
		t.Fatalf("expected 2 references, got %d", len(m.References))
	}
	for _, ref := range m.References {
		if ref.ToPath != "@"+ref.ToHash {
			t.Fatalf("reference not rewritten: %+v", ref)
		}
	}
}

func TestContentChange(t *testing.T) {
	src := setupSource(t)
	cache := t.TempDir()
	out1 := filepath.Join(t.TempDir(), "a.pkg")
	build(t, newPipeline(t, src, out1, cache, 4))

	// Modify one texture.
	makePNG(t, filepath.Join(src, "textures", "wood.png"), 16, 16, 99)

	out2 := filepath.Join(t.TempDir(), "b.pkg")
	r := build(t, newPipeline(t, src, out2, cache, 4))
	// The changed texture and its referencing descriptor miss; the other
	// texture hits.
	if r.Misses != 2 {
		t.Fatalf("expected 2 misses (texture + descriptor), got %d", r.Misses)
	}
	if r.Hits != 1 {
		t.Fatalf("expected 1 hit, got %d", r.Hits)
	}
}

// --- helpers ---

func copyDir(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o644)
	})
}

// craftPackageWithVersion rewrites the format version fields of a package
// while recomputing the footer hash.
func craftPackageWithVersion(t *testing.T, pkgPath string, formatVersion, minReader int) []byte {
	t.Helper()
	b, err := os.ReadFile(pkgPath)
	if err != nil {
		t.Fatal(err)
	}
	// Header: magic(5) + format(4) + minReader(4) + codec(4) + manifestLen(8)
	b[5] = byte(formatVersion)
	b[6] = byte(formatVersion >> 8)
	b[7] = byte(formatVersion >> 16)
	b[8] = byte(formatVersion >> 24)
	b[9] = byte(minReader)
	b[10] = byte(minReader >> 8)
	b[11] = byte(minReader >> 16)
	b[12] = byte(minReader >> 24)
	// Recompute footer.
	sum := sha256Sum(b[:len(b)-32])
	copy(b[len(b)-32:], sum)
	return b
}

func sha256Sum(b []byte) []byte {
	h := sha256.New()
	h.Write(b)
	return h.Sum(nil)
}

func TestEmptySource(t *testing.T) {
	dir := t.TempDir()
	cache := t.TempDir()
	out := filepath.Join(t.TempDir(), "empty.pkg")
	cfg := config.Default()
	cfg.Source = dir
	cfg.Output = out
	cfg.CacheDir = cache
	p, err := pipeline.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	report, err := p.Build(context.Background())
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if report.Total != 0 {
		t.Fatalf("expected 0 assets, got %d", report.Total)
	}
	m, err := pack.NewReader(out).ReadManifest()
	if err != nil {
		t.Fatal(err)
	}
	if m.AssetCount != 0 || m.BlobCount != 0 {
		t.Fatalf("expected empty manifest, got %d/%d", m.AssetCount, m.BlobCount)
	}
}

func TestCancellation(t *testing.T) {
	src := setupSource(t)
	cache := t.TempDir()
	out := filepath.Join(t.TempDir(), "cancelled.pkg")
	cfg := config.Default()
	cfg.Source = src
	cfg.Output = out
	cfg.CacheDir = cache
	cfg.Workers = 1
	p, err := pipeline.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately
	_, err = p.Build(ctx)
	if err == nil {
		t.Fatal("expected cancellation error")
	}
	if _, err := os.Stat(out); !os.IsNotExist(err) {
		t.Fatal("package should not exist after cancellation")
	}
}

func TestDescriptorNoRefs(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "plain.json"), `{"name": "plain", "value": 42}`)
	cache := t.TempDir()
	out := filepath.Join(t.TempDir(), "norefs.pkg")
	p := newPipeline(t, dir, out, cache, 4)
	r := build(t, p)
	if r.Errors != 0 {
		t.Fatalf("expected 0 errors, got %d", r.Errors)
	}
	m, err := pack.NewReader(out).ReadManifest()
	if err != nil {
		t.Fatal(err)
	}
	if len(m.References) != 0 {
		t.Fatalf("expected 0 references, got %d", len(m.References))
	}
}

func TestInterruptionRecovery(t *testing.T) {
	src := setupSource(t)
	cache := t.TempDir()

	// First build populates the cache.
	out1 := filepath.Join(t.TempDir(), "a.pkg")
	build(t, newPipeline(t, src, out1, cache, 4))

	// A cancelled build in between must not corrupt the cache.
	outCancelled := filepath.Join(t.TempDir(), "c.pkg")
	cfg := config.Default()
	cfg.Source = src
	cfg.Output = outCancelled
	cfg.CacheDir = cache
	cfg.Workers = 1
	p, _ := pipeline.New(cfg)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _ = p.Build(ctx) // expected to fail

	// Rebuild with a fresh context: cache entries from the first build are
	// still valid and reusable.
	out2 := filepath.Join(t.TempDir(), "b.pkg")
	r := build(t, newPipeline(t, src, out2, cache, 4))
	if r.Hits != 3 {
		t.Fatalf("expected 3 hits after interrupted build, got %d", r.Hits)
	}
	if !bytes.Equal(readPkg(t, out1), readPkg(t, out2)) {
		t.Fatal("package differs after interrupted build")
	}
}

func TestFailureIsolation(t *testing.T) {
	dir := t.TempDir()
	makePNG(t, filepath.Join(dir, "good1.png"), 8, 8, 1)
	makePNG(t, filepath.Join(dir, "good2.png"), 8, 8, 2)
	writeFile(t, filepath.Join(dir, "bad.png"), "not an image")

	cache := t.TempDir()
	out := filepath.Join(t.TempDir(), "out.pkg")
	cfg := config.Default()
	cfg.Source = dir
	cfg.Output = out
	cfg.CacheDir = cache
	p, _ := pipeline.New(cfg)
	report, err := p.Build(context.Background())
	if err == nil {
		t.Fatal("expected build error")
	}
	// Good assets were still processed and cached.
	if report == nil {
		t.Fatal("expected report")
	}
	if report.Errors != 1 {
		t.Fatalf("expected 1 error, got %d", report.Errors)
	}
	if report.Misses != 2 {
		t.Fatalf("expected 2 misses (good assets processed), got %d", report.Misses)
	}

	// Rebuild: good assets are cache hits; bad asset still errors.
	report2, err := p.Build(context.Background())
	if err == nil {
		t.Fatal("expected build error on rebuild")
	}
	if report2.Hits != 2 {
		t.Fatalf("expected 2 hits on rebuild, got %d", report2.Hits)
	}
	if report2.Errors != 1 {
		t.Fatalf("expected 1 error on rebuild, got %d", report2.Errors)
	}
}
