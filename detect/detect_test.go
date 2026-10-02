package detect_test

import (
	"bytes"
	"errors"
	"image"
	"image/color"
	"image/png"
	"strings"
	"testing"

	"github.com/shikejing/texture-asset-pipeline-go/detect"
)

func makePNG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	img.Set(0, 0, color.RGBA{R: 255, A: 255})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestIdentifyAndProbePNG(t *testing.T) {
	data := makePNG(t, 3, 5)
	f, err := detect.Identify("x.png", data)
	if err != nil || f != detect.FormatPNG {
		t.Fatalf("identify: %v %v", f, err)
	}
	info, err := detect.Probe("x.png", data)
	if err != nil {
		t.Fatal(err)
	}
	if info.Width != 3 || info.Height != 5 {
		t.Fatalf("dims=%dx%d", info.Width, info.Height)
	}
}

func TestUnknownFormat(t *testing.T) {
	_, err := detect.Identify("a.dat", []byte("hello world this is not a texture"))
	if !errors.Is(err, detect.ErrUnknownFormat) {
		t.Fatalf("want unknown format, got %v", err)
	}
	if se, ok := err.(*detect.StageError); !ok || se.Path != "a.dat" || se.Stage != "identify" {
		t.Fatalf("error must carry locatable path/stage: %v", err)
	}
}

func TestTruncatedPNG(t *testing.T) {
	_, err := detect.Probe("b.png", []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A, 0, 0})
	if !errors.Is(err, detect.ErrCorruptInput) {
		t.Fatalf("want corrupt input, got %v", err)
	}
	if !strings.Contains(err.Error(), "b.png") {
		t.Fatalf("error must name the asset: %v", err)
	}
}

func TestPPMValidation(t *testing.T) {
	good := append([]byte("P6\n2 2\n255\n"), make([]byte, 2*2*3)...)
	info, err := detect.Probe("c.ppm", good)
	if err != nil {
		t.Fatal(err)
	}
	if info.Width != 2 || info.Height != 2 || info.Format != detect.FormatPPM {
		t.Fatalf("info=%+v", info)
	}
	// Raster too short.
	if _, err := detect.Probe("d.ppm", []byte("P6\n2 2\n255\nabc")); err == nil {
		t.Fatal("short raster accepted")
	}
	// Trailing garbage.
	bad := append(good, 0)
	if _, err := detect.Probe("e.ppm", bad); err == nil {
		t.Fatal("trailing garbage accepted")
	}
	// Header escape attempts / illegal maxval.
	if _, err := detect.Probe("f.ppm", []byte("P6\n2 2\n70000\nxx")); err == nil {
		t.Fatal("illegal maxval accepted")
	}
}
