package pipeline_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/shikejing/texture-asset-pipeline-go/cache"
	"github.com/shikejing/texture-asset-pipeline-go/config"
	"github.com/shikejing/texture-asset-pipeline-go/manifest"
	"github.com/shikejing/texture-asset-pipeline-go/pack"
	"github.com/shikejing/texture-asset-pipeline-go/pipeline"
)

func buildOpen(t *testing.T, src string) (*pack.Reader, *pipeline.Report) {
	t.Helper()
	c, _ := cache.Open(filepath.Join(t.TempDir(), "cache"))
	out := filepath.Join(t.TempDir(), "out.pak")
	rep, err := pipeline.New(c).Build(context.Background(), src, out, config.Default(), config.Options{Jobs: 3})
	if err != nil {
		t.Fatal(err)
	}
	r, err := pack.Open(out)
	if err != nil {
		t.Fatal(err)
	}
	return r, rep
}

func TestContentDedupAcrossCopiesAndRenames(t *testing.T) {
	dir := t.TempDir()
	writePNG(t, filepath.Join(dir, "a.png"), 4, 4, pngRed)
	// Identical content under two names and a nested directory.
	if err := os.MkdirAll(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	writePNG(t, filepath.Join(dir, "sub", "copy.png"), 4, 4, pngRed)
	writePNG(t, filepath.Join(dir, "b.png"), 4, 4, pngBlue)

	r, _ := buildOpen(t, dir)
	defer r.Close()
	m := r.Manifest()
	if len(m.Assets) != 3 {
		t.Fatalf("assets=%d want 3", len(m.Assets))
	}
	if len(m.Blobs) != 2 {
		t.Fatalf("identical content must dedup: blobs=%d want 2", len(m.Blobs))
	}
	ids := map[string]int{}
	for _, a := range m.Assets {
		ids[a.Blob]++
	}
	if ids[m.Blobs[0].ID]+ids[m.Blobs[1].ID] != 3 {
		t.Fatal("all assets must reference one of the two blobs")
	}

	// Rename/move one copy: same two blobs, same total bytes.
	before, _ := os.ReadFile(filepath.Join(dir, "a.png"))
	if err := os.WriteFile(filepath.Join(dir, "moved", "x.png"), before, 0o644); err != nil {
		if !os.IsNotExist(err) {
			t.Fatal(err)
		}
		os.MkdirAll(filepath.Join(dir, "moved"), 0o755)
		if err := os.WriteFile(filepath.Join(dir, "moved", "x.png"), before, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Remove(filepath.Join(dir, "a.png")); err != nil {
		t.Fatal(err)
	}
	r2, _ := buildOpen(t, dir)
	defer r2.Close()
	if len(r2.Manifest().Blobs) != 2 {
		t.Fatalf("rename/move must not create duplicate data: blobs=%d", len(r2.Manifest().Blobs))
	}
}

func TestLinkSidecarAndRefResolution(t *testing.T) {
	dir := t.TempDir()
	writePNG(t, filepath.Join(dir, "real.png"), 4, 4, pngRed)
	writePNG(t, filepath.Join(dir, "other.png"), 4, 4, pngBlue)
	// "alias.png" does not exist as an image; its sidecar declares a link.
	link := `{"kind":"link","target":"real.png"}`
	if err := os.WriteFile(filepath.Join(dir, "alias.png.refs.json"), []byte(link), 0o644); err != nil {
		t.Fatal(err)
	}
	// other.png references real.png relatively.
	ref := `{"kind":"refs","refs":[{"kind":"dependency","path":"real.png"}]}`
	if err := os.WriteFile(filepath.Join(dir, "other.png.refs.json"), []byte(ref), 0o644); err != nil {
		t.Fatal(err)
	}

	r, rep := buildOpen(t, dir)
	defer r.Close()
	m := r.Manifest()

	if len(m.Assets) != 3 {
		t.Fatalf("assets=%d want 3", len(m.Assets))
	}
	var alias, other *manifest.Asset
	var realID string
	for i := range m.Assets {
		switch m.Assets[i].Path {
		case "alias.png":
			alias = &m.Assets[i]
		case "other.png":
			other = &m.Assets[i]
		case "real.png":
			realID = m.Assets[i].Blob
		}
	}
	if alias == nil || other == nil || realID == "" {
		t.Fatalf("missing expected assets: %+v %+v", alias, other)
	}
	if alias.Type != manifest.TypeLink {
		t.Fatalf("alias type=%s want link", alias.Type)
	}
	if alias.Blob != realID {
		t.Fatal("link must resolve to target content blob")
	}
	foundLink := false
	for _, rf := range alias.Refs {
		if rf.Kind == manifest.RefLink && rf.Path == "real.png" && rf.TargetBlob == realID {
			foundLink = true
		}
	}
	if !foundLink {
		t.Fatalf("link ref not resolved to blob: %+v", alias.Refs)
	}
	foundDep := false
	for _, rf := range other.Refs {
		if rf.Kind == manifest.RefDependency && rf.Path == "real.png" && rf.TargetBlob == realID {
			foundDep = true
		}
	}
	if !foundDep {
		t.Fatalf("dependency ref not resolved to blob: %+v", other.Refs)
	}
	// Only real+other payloads exist; alias contributes no new blob.
	if len(m.Blobs) != 2 {
		t.Fatalf("blobs=%d want 2 (link stores nothing)", len(m.Blobs))
	}
	st := statusMap(rep)
	if st["alias.png"] != pipeline.StatusLink {
		t.Fatalf("alias status=%s want link", st["alias.png"])
	}
}
