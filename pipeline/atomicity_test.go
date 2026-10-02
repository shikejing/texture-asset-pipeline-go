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

func TestFailedBuildKeepsExistingPack(t *testing.T) {
	dir := t.TempDir()
	writePNG(t, filepath.Join(dir, "good.png"), 4, 4, pngRed)
	c, _ := cache.Open(filepath.Join(t.TempDir(), "cache"))
	out := filepath.Join(t.TempDir(), "game.pak")
	if _, err := pipeline.New(c).Build(context.Background(), dir, out, config.Default(), config.Options{Jobs: 2}); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}

	// Add a bad asset and rebuild strictly; build must fail and the old,
	// previously published pack must remain byte-identical on disk.
	if err := os.WriteFile(filepath.Join(dir, "bad.png"), []byte("not a texture!!"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := pipeline.New(c).Build(context.Background(), dir, out, config.Default(), config.Options{Jobs: 2}); err == nil {
		t.Fatal("build should fail")
	}
	after, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("existing pack vanished: %v", err)
	}
	if string(before) != string(after) {
		t.Fatal("failed build must not modify the existing pack")
	}
}

func TestUnchangedInputReproIsIdenticalWithDifferentReportOrder(t *testing.T) {
	// High concurrency repeatedly; content/order guarantees still hold.
	colors := []color.RGBA{
		{R: 1, A: 255}, {G: 2, A: 255}, {B: 3, A: 255},
		{R: 4, A: 255}, {R: 5, A: 255}, {R: 6, A: 255},
	}
	dir := t.TempDir()
	for i, c := range colors {
		writePNG(t, filepath.Join(dir, string(rune('a'+i))+".png"), 5, 5, c)
	}
	var ref []byte
	for run := 0; run < 5; run++ {
		got := buildBytes(t, dir, filepath.Join(t.TempDir(), "cc"), 16)
		if ref == nil {
			ref = got
			continue
		}
		if string(got) != string(ref) {
			t.Fatalf("run %d diverged", run)
		}
	}
}

var _ = context.Background
