package pipeline_test

import (
	"bytes"
	"context"
	"path/filepath"
	"testing"

	"github.com/shikejing/texture-asset-pipeline-go/cache"
	"github.com/shikejing/texture-asset-pipeline-go/config"
	"github.com/shikejing/texture-asset-pipeline-go/pack"
	"github.com/shikejing/texture-asset-pipeline-go/pipeline"
)

func TestBuildSmoke(t *testing.T) {
	src := newSrc(t)
	c, err := cache.Open(filepath.Join(t.TempDir(), "cache"))
	if err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "out.pak")
	rep, err := pipeline.New(c).Build(context.Background(), src, out, config.Default(), config.Options{Jobs: 1})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if len(rep.Reports) != 2 {
		t.Fatalf("reports = %d, want 2", len(rep.Reports))
	}
	r, err := pack.Open(out)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	m := r.Manifest()
	if len(m.Assets) != 2 {
		t.Fatalf("assets = %d, want 2", len(m.Assets))
	}
	if len(m.Blobs) != 2 {
		t.Fatalf("blobs = %d, want 2", len(m.Blobs))
	}
	if m.Assets[0].Path != "blue.png" || m.Assets[1].Path != "red.png" {
		t.Fatalf("asset order not sorted: %s, %s", m.Assets[0].Path, m.Assets[1].Path)
	}
	for _, b := range m.Blobs {
		payload, err := r.Blob(b.ID)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.HasPrefix(payload, []byte{'F', 'T', 'X', '1'}) {
			t.Fatalf("blob %s missing codec frame", b.ID)
		}
	}
}
