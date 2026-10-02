package pipeline_test

import (
	"context"
	"image/color"
	"os"
	"path/filepath"
	"testing"

	"github.com/shikejing/texture-asset-pipeline-go/cache"
	"github.com/shikejing/texture-asset-pipeline-go/config"
	"github.com/shikejing/texture-asset-pipeline-go/pipeline"
)

func statusMap(rep *pipeline.Report) map[string]pipeline.Status {
	m := make(map[string]pipeline.Status)
	for _, r := range rep.Reports {
		m[r.Path] = r.Status
	}
	return m
}

func TestIncrementalBuildMatchesCleanBuild(t *testing.T) {
	src := newSrc(t)
	cacheDir := filepath.Join(t.TempDir(), "cache")

	// Full build 1: everything processed.
	out1 := filepath.Join(t.TempDir(), "a.pak")
	c1, _ := cache.Open(cacheDir)
	p := pipeline.New(c1)
	rep1, err := p.Build(context.Background(), src, out1, config.Default(), config.Options{Jobs: 4})
	if err != nil {
		t.Fatal(err)
	}
	sm := statusMap(rep1)
	if sm["red.png"] != pipeline.StatusProcessed || sm["blue.png"] != pipeline.StatusProcessed {
		t.Fatalf("first build should process all, got %v", sm)
	}
	if n := cacheDirFiles(t, cacheDir); n < 2 {
		t.Fatalf("expected >=2 cache entries, got %d", n)
	}

	// Build 2, same cache: everything cached.
	out2 := filepath.Join(t.TempDir(), "b.pak")
	rep2, err := p.Build(context.Background(), src, out2, config.Default(), config.Options{Jobs: 4})
	if err != nil {
		t.Fatal(err)
	}
	sm2 := statusMap(rep2)
	if sm2["red.png"] != pipeline.StatusCached || sm2["blue.png"] != pipeline.StatusCached {
		t.Fatalf("second build should hit cache, got %v", sm2)
	}

	b1, _ := os.ReadFile(out1)
	b2, _ := os.ReadFile(out2)
	if string(b1) != string(b2) {
		t.Fatal("incremental pack differs from full pack")
	}

	// Change only red.png: red reprocessed, blue stays cached; bytes equal
	// a clean build of the new tree.
	writePNG(t, filepath.Join(src, "red.png"), 4, 4, color.RGBA{R: 1, G: 2, B: 3, A: 255})
	out3 := filepath.Join(t.TempDir(), "c.pak")
	rep3, err := p.Build(context.Background(), src, out3, config.Default(), config.Options{Jobs: 4})
	if err != nil {
		t.Fatal(err)
	}
	sm3 := statusMap(rep3)
	if sm3["red.png"] != pipeline.StatusProcessed {
		t.Fatalf("changed asset should reprocess, got %v", sm3)
	}
	if sm3["blue.png"] != pipeline.StatusCached {
		t.Fatalf("unchanged asset should be cached, got %v", sm3)
	}

	freshSrc := copyTree(t, src)
	bClean := buildBytes(t, freshSrc, filepath.Join(t.TempDir(), "fresh"), 4)
	b3, _ := os.ReadFile(out3)
	if string(b3) != string(bClean) {
		t.Fatal("incremental output does not match clean rebuild")
	}
}

func TestConfigChangeInvalidatesCache(t *testing.T) {
	src := newSrc(t)
	cacheDir := filepath.Join(t.TempDir(), "cache")
	out := filepath.Join(t.TempDir(), "a.pak")
	c, _ := cache.Open(cacheDir)
	p := pipeline.New(c)
	cfg := config.Default()
	if _, err := p.Build(context.Background(), src, out, cfg, config.Options{Jobs: 2}); err != nil {
		t.Fatal(err)
	}
	cfg2 := cfg
	cfg2.CompressionLevel = 9
	rep, err := p.Build(context.Background(), src, filepath.Join(t.TempDir(), "b.pak"), cfg2, config.Options{Jobs: 2})
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rep.Reports {
		if r.Status != pipeline.StatusProcessed {
			t.Fatalf("config change must invalidate cache; %s was %s", r.Path, r.Status)
		}
	}
}

func cacheDirFiles(t *testing.T, dir string) int {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(dir, "results"))
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, e := range entries {
		if !e.IsDir() {
			n++
		}
	}
	return n
}
