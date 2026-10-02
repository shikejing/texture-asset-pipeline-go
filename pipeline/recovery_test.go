package pipeline_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shikejing/texture-asset-pipeline-go/cache"
	"github.com/shikejing/texture-asset-pipeline-go/config"
	"github.com/shikejing/texture-asset-pipeline-go/pipeline"
)

func TestBuildRecoversFromCorruptCache(t *testing.T) {
	src := newSrc(t)
	cacheDir := filepath.Join(t.TempDir(), "cache")
	out1 := filepath.Join(t.TempDir(), "a.pak")
	c, _ := cache.Open(cacheDir)
	p := pipeline.New(c)
	if _, err := p.Build(context.Background(), src, out1, config.Default(), config.Options{Jobs: 2}); err != nil {
		t.Fatal(err)
	}

	// Corrupt one cache entry: flip bytes in the largest result file.
	entries, err := os.ReadDir(filepath.Join(cacheDir, "results"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) == 0 {
		t.Fatal("no cache entries written")
	}
	var target string
	var maxSize int64
	for _, e := range entries {
		info, _ := e.Info()
		if info.Size() > maxSize {
			maxSize = info.Size()
			target = filepath.Join(cacheDir, "results", e.Name())
		}
	}
	b, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	b[len(b)-5] ^= 0xAA
	b[len(b)-6] ^= 0x55
	if err := os.WriteFile(target, b, 0o644); err != nil {
		t.Fatal(err)
	}

	// Rebuild: the corrupt entry is purged, its asset reprocessed, and the
	// final pack is still byte-identical to a clean build.
	out2 := filepath.Join(t.TempDir(), "b.pak")
	rep, err := p.Build(context.Background(), src, out2, config.Default(), config.Options{Jobs: 2})
	if err != nil {
		t.Fatalf("rebuild must recover from corrupt cache: %v", err)
	}
	reprocessed := 0
	for _, r := range rep.Reports {
		if r.Status == pipeline.StatusProcessed {
			reprocessed++
		}
	}
	if reprocessed != 1 {
		t.Fatalf("exactly one asset should be reprocessed, got %d", reprocessed)
	}
	b1, _ := os.ReadFile(out1)
	b2, _ := os.ReadFile(out2)
	if string(b1) != string(b2) {
		t.Fatal("recovered build must be byte-identical to the original")
	}
}

func TestCacheDirectoryCanBeReusedAcrossPipelines(t *testing.T) {
	src := newSrc(t)
	cacheDir := filepath.Join(t.TempDir(), "cache")
	build := func() *pipeline.Report {
		c, err := cache.Open(cacheDir)
		if err != nil {
			t.Fatal(err)
		}
		out := filepath.Join(t.TempDir(), "out.pak")
		rep, err := pipeline.New(c).Build(context.Background(), src, out, config.Default(), config.Options{Jobs: 2})
		if err != nil {
			t.Fatal(err)
		}
		return rep
	}
	_ = build()
	rep := build() // new Pipeline instance, same persistent cache
	for _, r := range rep.Reports {
		if r.Status != pipeline.StatusCached {
			t.Fatalf("second pipeline should reuse on-disk cache; %s=%s", r.Path, r.Status)
		}
	}
}

func TestInvalidConfigRejected(t *testing.T) {
	cases := []string{"", "bad-codec", "bad-level", "bad-onerror"}
	cfgs := []config.Config{
		{Codec: "", CompressionLevel: 6, OnError: config.OnErrorStrict},
		{Codec: "nope", CompressionLevel: 6},
		{Codec: config.CodecFlate, CompressionLevel: 42},
		{Codec: config.CodecFlate, CompressionLevel: 6, OnError: "explode"},
	}
	for i, cfg := range cfgs {
		c, _ := cache.Open(filepath.Join(t.TempDir(), "c"))
		_, err := pipeline.New(c).Build(context.Background(), t.TempDir(),
			filepath.Join(t.TempDir(), "o.pak"), cfg, config.Options{})
		if err == nil || !strings.Contains(err.Error(), "config") {
			t.Fatalf("case %s (%d): expected config error, got %v", cases[i], i, err)
		}
	}
}
