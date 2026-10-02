package pipeline_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/shikejing/texture-asset-pipeline-go/cache"
	"github.com/shikejing/texture-asset-pipeline-go/config"
	"github.com/shikejing/texture-asset-pipeline-go/detect"
	"github.com/shikejing/texture-asset-pipeline-go/pipeline"
)

func TestStrictBuildFailsOnBadAsset(t *testing.T) {
	dir := t.TempDir()
	writePNG(t, filepath.Join(dir, "good.png"), 4, 4, pngRed)
	if err := os.WriteFile(filepath.Join(dir, "bad.png"), []byte{0x89, 'P', 'N', 'G', 0, 1, 2, 3}, 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "out.pak")
	c, _ := cache.Open(filepath.Join(t.TempDir(), "cache"))
	_, err := pipeline.New(c).Build(context.Background(), dir, out, config.Default(), config.Options{Jobs: 4})
	if err == nil {
		t.Fatal("strict build must fail on corrupt asset")
	}
	if !errors.Is(err, detect.ErrCorruptInput) && !errors.Is(err, detect.ErrUnknownFormat) {
		t.Fatalf("error must locate decode failure, got %v", err)
	}
	if _, statErr := os.Stat(out); !os.IsNotExist(statErr) {
		t.Fatal("no partial pack may be left after a failed strict build")
	}
}

func TestLenientBuildSkipsBadAsset(t *testing.T) {
	dir := t.TempDir()
	writePNG(t, filepath.Join(dir, "good.png"), 4, 4, pngRed)
	// Unknown magic.
	if err := os.WriteFile(filepath.Join(dir, "weird.bin"), []byte("????not-a-texture"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Truncated PNG: right magic, broken body.
	if err := os.WriteFile(filepath.Join(dir, "trunc.png"),
		[]byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A, 0, 0, 0}, 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "out.pak")
	c, _ := cache.Open(filepath.Join(t.TempDir(), "cache"))
	cfg := config.Default()
	cfg.OnError = config.OnErrorLenient
	rep, err := pipeline.New(c).Build(context.Background(), dir, out, cfg, config.Options{Jobs: 4})
	if err != nil {
		t.Fatalf("lenient build should not fail: %v", err)
	}
	if len(rep.Failed()) != 2 {
		t.Fatalf("want 2 failed assets, got %d: %+v", len(rep.Failed()), rep.Reports)
	}
	if len(rep.Diagnostics) != 2 {
		t.Fatalf("want 2 diagnostics, got %d", len(rep.Diagnostics))
	}
	for _, d := range rep.Diagnostics {
		if d.Path == "" || d.Stage == "" || d.Reason == "" {
			t.Fatalf("diagnostic must be locatable: %+v", d)
		}
	}
	// The good asset still loads; bad ones never enter the manifest.
	r, err := openPack(out)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if len(r.Manifest().Assets) != 1 || r.Manifest().Assets[0].Path != "good.png" {
		t.Fatalf("bad assets must not pollute manifest, got %+v", r.Manifest().Assets)
	}
}

func TestPerAssetTimeoutFailsWithoutLeavingPack(t *testing.T) {
	dir := t.TempDir()
	const n = 512
	header := []byte(fmt.Sprintf("P6\n%d %d\n255\n", n, n))
	raster := make([]byte, n*n*3)
	xorshift := uint32(42)
	for i := range raster {
		xorshift ^= xorshift << 13
		xorshift ^= xorshift >> 17
		xorshift ^= xorshift << 5
		raster[i] = byte(xorshift)
	}
	ppm := append(header, raster...)
	if err := os.WriteFile(filepath.Join(dir, "big.ppm"), ppm, 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "out.pak")
	c, _ := cache.Open(filepath.Join(t.TempDir(), "cache"))
	start := time.Now()
	_, err := pipeline.New(c).Build(context.Background(), dir, out, config.Default(), config.Options{
		Jobs: 1, PerAssetTimeoutMS: 1,
	})
	if err == nil {
		t.Fatal("asset exceeding timeout must fail the strict build")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("want deadline exceeded, got %v", err)
	}
	if time.Since(start) > 10*time.Second {
		t.Fatal("timeout was not enforced promptly")
	}
	if _, statErr := os.Stat(out); !os.IsNotExist(statErr) {
		t.Fatal("timed-out build must leave no pack behind")
	}
}

func TestPerAssetTimeoutAndBuildCancellation(t *testing.T) {
	dir := t.TempDir()
	// Highly incompressible noise-like content to keep the busy loop busy.
	big := make([]byte, 2*1024*1024)
	xorshift := uint32(2463534242)
	for i := range big {
		xorshift ^= xorshift << 13
		xorshift ^= xorshift >> 17
		xorshift ^= xorshift << 5
		big[i] = byte(xorshift)
	}
	// Valid PPM: header + raster.
	ppm := append([]byte("P6\n64 64\n255\n"), make([]byte, 64*64*3)...)
	copy(ppm[len("P6\n64 64\n255\n"):], big)
	if err := os.WriteFile(filepath.Join(dir, "a.ppm"), ppm, 0o644); err != nil {
		t.Fatal(err)
	}
	for i := 1; i < 6; i++ {
		writePNG(t, filepath.Join(dir, fmt.Sprintf("g%d.png", i)), 8, 8, pngRed)
	}

	out := filepath.Join(t.TempDir(), "out.pak")
	c, _ := cache.Open(filepath.Join(t.TempDir(), "cache"))

	ctx, cancel := context.WithCancel(context.Background())
	var cancelled int32
	go func() {
		time.Sleep(5 * time.Millisecond)
		atomic.StoreInt32(&cancelled, 1)
		cancel()
	}()
	_, err := pipeline.New(c).Build(ctx, dir, out, config.Default(), config.Options{
		Jobs: 2, PerAssetTimeoutMS: 5000,
	})
	if err == nil {
		t.Fatal("cancelled build must return an error")
	}
	if atomic.LoadInt32(&cancelled) != 1 {
		t.Fatal("test harness did not cancel")
	}
	if _, statErr := os.Stat(out); !os.IsNotExist(statErr) {
		t.Fatal("cancelled build must leave no pack behind")
	}
}
