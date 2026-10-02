package codec

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"testing"
)

func TestSanity(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 8, 8))
	for y := 0; y < 8; y++ {
		for x := 0; x < 8; x++ {
			img.Set(x, y, color.NRGBA{uint8(x * 30), uint8(y * 30), 128, 255})
		}
	}
	var buf bytes.Buffer
	png.Encode(&buf, img)
	tex, err := Decode(&buf)
	if err != nil {
		t.Fatal(err)
	}
	p, err := Compress(tex, 6)
	if err != nil {
		t.Fatal(err)
	}
	enc, err := Encode(p)
	if err != nil {
		t.Fatal(err)
	}
	dec, err := DecodeContainer(enc)
	if err != nil {
		t.Fatal(err)
	}
	if dec.Width != 8 || dec.Height != 8 || dec.Format != PixelFormatBC1 {
		t.Fatalf("bad decode: %+v", dec)
	}
	if len(dec.Data) != 4*8 {
		t.Fatalf("bad data len: %d", len(dec.Data))
	}
}

func TestDeterminism(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 13, 17))
	for y := 0; y < 17; y++ {
		for x := 0; x < 13; x++ {
			img.Set(x, y, color.NRGBA{uint8(x * 7), uint8(y * 11), uint8(x + y), 255})
		}
	}
	var buf bytes.Buffer
	png.Encode(&buf, img)
	tex, err := Decode(&buf)
	if err != nil {
		t.Fatal(err)
	}
	p1, err := Compress(tex, 6)
	if err != nil {
		t.Fatal(err)
	}
	p2, err := Compress(tex, 6)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(p1.Data, p2.Data) {
		t.Fatal("compress is not deterministic")
	}
	e1, _ := Encode(p1)
	e2, _ := Encode(p2)
	if !bytes.Equal(e1, e2) {
		t.Fatal("encode is not deterministic")
	}
}

func TestAlpha(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 4, 4))
	for y := 0; y < 4; y++ {
		for x := 0; x < 4; x++ {
			img.Set(x, y, color.NRGBA{255, 0, 0, uint8(x * 40)})
		}
	}
	var buf bytes.Buffer
	png.Encode(&buf, img)
	tex, err := Decode(&buf)
	if err != nil {
		t.Fatal(err)
	}
	p, err := Compress(tex, 6)
	if err != nil {
		t.Fatal(err)
	}
	if !p.HasAlpha {
		t.Fatal("expected HasAlpha=true")
	}
}
