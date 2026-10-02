package pack_test

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/shikejing/texture-asset-pipeline-go/config"
	"github.com/shikejing/texture-asset-pipeline-go/manifest"
	"github.com/shikejing/texture-asset-pipeline-go/pack"
)

func buildPack(t *testing.T) ([]byte, *manifest.Manifest) {
	t.Helper()
	m := &manifest.Manifest{
		FormatVersion:    manifest.FormatVersion,
		AlgorithmVersion: config.AlgorithmVersion,
		Codec:            "flate-v1",
	}
	entries := []pack.Entry{
		{ID: "01", Data: []byte("payload-one")},
		{ID: "02", Data: []byte("payload-two-longer")},
	}
	for _, e := range entries {
		m.Blobs = append(m.Blobs, manifest.Blob{
			ID: e.ID, Codec: "flate-v1",
			Size: int64(len(e.Data)), RawSize: int64(len(e.Data)) * 2,
			Hash: hashOf(e.Data),
		})
	}
	m.Assets = []manifest.Asset{
		{Path: "a", Type: "png", Blob: "01", Size: int64(len(entries[0].Data)), RawHash: "r1"},
		{Path: "b", Type: "png", Blob: "02", Size: int64(len(entries[1].Data)), RawHash: "r2"},
	}
	var buf bytes.Buffer
	if err := pack.WriteTo(&buf, m, entries); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes(), m
}

func TestRoundTrip(t *testing.T) {
	data, m := buildPack(t)
	r, err := pack.Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Manifest().Assets) != 2 {
		t.Fatal("assets lost in round trip")
	}
	if r.Manifest().Codec != m.Codec {
		t.Fatal("codec field mismatch")
	}
	for _, id := range []string{"01", "02"} {
		want, _ := r.Blob(id)
		if len(want) == 0 {
			t.Fatalf("blob %s empty", id)
		}
	}
}

func TestBitFlipAnywhereIsRejected(t *testing.T) {
	good, _ := buildPack(t)
	for _, pos := range []int{0, 5, 30, len(good) - 1, len(good) / 2} {
		bad := bytes.Clone(good)
		bad[pos] ^= 0xFF
		if _, err := pack.Parse(bad); err == nil {
			t.Fatalf("bit flip at %d was accepted", pos)
		}
	}
}

func TestTruncationAndTrailingGarbageRejected(t *testing.T) {
	good, _ := buildPack(t)
	for _, cut := range []int{1, 10, 83, 84, len(good) - 1} {
		if _, err := pack.Parse(good[:cut]); err == nil {
			t.Fatalf("truncation to %d accepted", cut)
		}
	}
	trailing := append(bytes.Clone(good), 0x00)
	if _, err := pack.Parse(trailing); err == nil {
		t.Fatal("trailing garbage accepted")
	}
}

func TestBadMagicRejected(t *testing.T) {
	good, _ := buildPack(t)
	good[0] = 'X'
	if _, err := pack.Parse(good); !errors.Is(err, pack.ErrBadMagic) {
		t.Fatalf("want ErrBadMagic, got %v", err)
	}
}

func TestVersionCompatibility(t *testing.T) {
	if err := manifest.CheckCompatibility(&manifest.Manifest{FormatVersion: 1}, 1); err != nil {
		t.Fatalf("v1 reader must read v1: %v", err)
	}
	err := manifest.CheckCompatibility(&manifest.Manifest{FormatVersion: 2}, 1)
	if err == nil || !errors.Is(err, manifest.ErrUnsupportedVersion) {
		t.Fatalf("v1 reader must reject v2, got %v", err)
	}
	if !strings.Contains(err.Error(), "newer") {
		t.Fatalf("error should distinguish cause: %v", err)
	}
	err = manifest.CheckCompatibility(&manifest.Manifest{FormatVersion: 0}, 1)
	if err == nil || !errors.Is(err, manifest.ErrUnsupportedVersion) {
		t.Fatalf("ancient version must be rejected, got %v", err)
	}
	if err := manifest.CheckCompatibility(&manifest.Manifest{FormatVersion: 1}, 3); err != nil {
		t.Fatalf("forward-capable reader must accept old packs: %v", err)
	}
}
