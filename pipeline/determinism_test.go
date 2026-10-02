package pipeline_test

import (
	"context"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/shikejing/texture-asset-pipeline-go/cache"
	"github.com/shikejing/texture-asset-pipeline-go/config"
	"github.com/shikejing/texture-asset-pipeline-go/pipeline"
)

// writePNG renders a deterministic solid-color PNG at dst.
func writePNG(t *testing.T, dst string, w, h int, c color.RGBA) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.SetRGBA(x, y, c)
		}
	}
	f, err := os.Create(dst)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
}

func newSrc(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	writePNG(t, filepath.Join(dir, "red.png"), 4, 4, color.RGBA{R: 255, A: 255})
	writePNG(t, filepath.Join(dir, "blue.png"), 4, 4, color.RGBA{B: 255, A: 255})
	return dir
}

// copyTree duplicates a source tree into a fresh temp dir.
func copyTree(t *testing.T, src string) string {
	t.Helper()
	dst := t.TempDir()
	err := filepath.Walk(src, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, info.Mode())
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, info.Mode())
	})
	if err != nil {
		t.Fatal(err)
	}
	return dst
}

func buildBytes(t *testing.T, src, cacheDir string, jobs int) []byte {
	t.Helper()
	var c *cache.Cache
	var err error
	if cacheDir != "" {
		c, err = cache.Open(cacheDir)
		if err != nil {
			t.Fatal(err)
		}
	}
	out := filepath.Join(t.TempDir(), "out.pak")
	_, err = pipeline.New(c).Build(context.Background(), src, out, config.Default(), config.Options{Jobs: jobs})
	if err != nil {
		t.Fatalf("build jobs=%d: %v", jobs, err)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestDeterminismAcrossRepeatsAndParallelism(t *testing.T) {
	src := newSrc(t)
	baseline := buildBytes(t, src, filepath.Join(t.TempDir(), "c1"), 1)

	if got := buildBytes(t, src, filepath.Join(t.TempDir(), "c2"), 1); string(got) != string(baseline) {
		t.Fatal("rebuild with fresh cache produced different bytes")
	}
	for _, jobs := range []int{1, 2, 4, 16} {
		got := buildBytes(t, src, filepath.Join(t.TempDir(), "cj"), jobs)
		if string(got) != string(baseline) {
			t.Fatalf("jobs=%d produced different bytes", jobs)
		}
	}
	src2 := copyTree(t, src)
	got := buildBytes(t, src2, filepath.Join(t.TempDir(), "c3"), 8)
	if string(got) != string(baseline) {
		t.Fatal("identical content in different dir produced different bytes")
	}
}

func TestDeterminismShuffledInsertion(t *testing.T) {
	colors := []color.RGBA{
		{R: 255, A: 255}, {G: 255, A: 255}, {B: 255, A: 255},
		{R: 10, G: 20, B: 30, A: 255}, {R: 200, G: 100, B: 50, A: 255},
	}
	mk := func(order []int) string {
		dir := t.TempDir()
		for _, i := range order {
			writePNG(t, filepath.Join(dir, string(rune('a'+i))+".png"), 3, 3, colors[i])
		}
		return dir
	}
	d1 := mk([]int{4, 2, 0, 3, 1})
	d2 := mk([]int{1, 3, 0, 2, 4})
	b1 := buildBytes(t, d1, "", 1)
	b2 := buildBytes(t, d2, "", 8)
	if string(b1) != string(b2) {
		t.Fatal("file creation order leaked into output bytes")
	}
}
