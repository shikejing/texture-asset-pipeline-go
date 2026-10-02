package manifest_test

import (
	"strings"
	"testing"

	"github.com/shikejing/texture-asset-pipeline-go/config"
	"github.com/shikejing/texture-asset-pipeline-go/manifest"
)

func TestCanonicalJSONIsStable(t *testing.T) {
	m := &manifest.Manifest{
		FormatVersion: manifest.FormatVersion, AlgorithmVersion: config.AlgorithmVersion,
		Codec: "flate-v1",
		Blobs: []manifest.Blob{{ID: "z", Codec: "flate-v1", Size: 1, RawSize: 2, Hash: "h"}},
		Assets: []manifest.Asset{
			{Path: "a", Type: "png", Blob: "z", Size: 1, RawHash: "r"},
		},
	}
	b1, err := manifest.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	b2, _ := manifest.Marshal(m)
	if string(b1) != string(b2) {
		t.Fatal("marshal not stable")
	}
	if !strings.HasSuffix(string(b1), "\n") {
		t.Fatal("canonical json should end with newline")
	}
	parsed, err := manifest.Unmarshal(b1)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Assets[0].Path != "a" || parsed.Blobs[0].ID != "z" {
		t.Fatal("unmarshal mismatch")
	}
}
