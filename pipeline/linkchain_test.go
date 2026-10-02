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

func writeSidecar(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLinkChainAndRefThroughLink(t *testing.T) {
	dir := t.TempDir()
	writePNG(t, filepath.Join(dir, "real.png"), 4, 4, pngRed)
	// b -> real, c -> b (link chain); other references the alias b.
	writeSidecar(t, dir, "b.png.refs.json", `{"kind":"link","target":"real.png"}`)
	writeSidecar(t, dir, "c.png.refs.json", `{"kind":"link","target":"b.png"}`)
	writePNG(t, filepath.Join(dir, "other.png"), 4, 4, pngBlue)
	writeSidecar(t, dir, "other.png.refs.json",
		`{"kind":"refs","refs":[{"kind":"dependency","path":"b.png"}]}`)

	c, _ := cache.Open(filepath.Join(t.TempDir(), "cache"))
	out := filepath.Join(t.TempDir(), "out.pak")
	rep, err := pipeline.New(c).Build(context.Background(), dir, out, config.Default(), config.Options{Jobs: 3})
	if err != nil {
		t.Fatal(err)
	}
	st := statusMap(rep)
	if st["b.png"] != pipeline.StatusLink || st["c.png"] != pipeline.StatusLink {
		t.Fatalf("chain links not recognized: %v", st)
	}
	r, err := pack.Open(out)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	m := r.Manifest()
	byPath := map[string]manifest.Asset{}
	for _, a := range m.Assets {
		byPath[a.Path] = a
	}
	real := byPath["real.png"]
	if byPath["b.png"].Blob != real.Blob || byPath["c.png"].Blob != real.Blob {
		t.Fatal("link chain must resolve to the real payload blob")
	}
	var dep *manifest.Ref
	for i := range byPath["other.png"].Refs {
		if byPath["other.png"].Refs[i].Kind == manifest.RefDependency {
			dep = &byPath["other.png"].Refs[i]
		}
	}
	if dep == nil || dep.Path != "b.png" || dep.TargetBlob != real.Blob {
		t.Fatalf("ref through link must resolve to final blob: %+v", dep)
	}
}

func TestLinkCycleRejected(t *testing.T) {
	dir := t.TempDir()
	writePNG(t, filepath.Join(dir, "root.png"), 4, 4, pngRed)
	writeSidecar(t, dir, "a.png.refs.json", `{"kind":"link","target":"b.png"}`)
	writeSidecar(t, dir, "b.png.refs.json", `{"kind":"link","target":"a.png"}`)

	// Strict: cycle fails the build and leaves no pack.
	out := filepath.Join(t.TempDir(), "out.pak")
	c, _ := cache.Open(filepath.Join(t.TempDir(), "cache"))
	if _, err := pipeline.New(c).Build(context.Background(), dir, out, config.Default(), config.Options{}); err == nil {
		t.Fatal("link cycle must fail a strict build")
	}
	if _, err := os.Stat(out); !os.IsNotExist(err) {
		t.Fatal("failed build must leave no pack")
	}

	// Lenient: the cycle links are dropped, real asset still builds.
	cfg := config.Default()
	cfg.OnError = config.OnErrorLenient
	out2 := filepath.Join(t.TempDir(), "out2.pak")
	rep, err := pipeline.New(c).Build(context.Background(), dir, out2, cfg, config.Options{})
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rep.Reports {
		if r.Path == "root.png" && r.Status != pipeline.StatusProcessed {
			t.Fatalf("real asset should build, got %s", r.Status)
		}
	}
	r, err := pack.Open(out2)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if len(r.Manifest().Assets) != 1 || r.Manifest().Assets[0].Path != "root.png" {
		t.Fatalf("cycle links must not enter manifest: %+v", r.Manifest().Assets)
	}
}

func TestDanglingLinkAndRefFailures(t *testing.T) {
	dir := t.TempDir()
	writePNG(t, filepath.Join(dir, "ok.png"), 4, 4, pngRed)
	writeSidecar(t, dir, "ghost.png.refs.json", `{"kind":"link","target":"missing.png"}`)
	writePNG(t, filepath.Join(dir, "d.png"), 4, 4, pngBlue)
	writeSidecar(t, dir, "d.png.refs.json",
		`{"kind":"refs","refs":[{"kind":"dependency","path":"missing.png"}]}`)

	cfg := config.Default()
	cfg.OnError = config.OnErrorLenient
	out := filepath.Join(t.TempDir(), "out.pak")
	c2, _ := cache.Open(filepath.Join(t.TempDir(), "cache2"))
	rep, err := pipeline.New(c2).Build(context.Background(), dir, out, cfg, config.Options{})
	if err != nil {
		t.Fatal(err)
	}
	st := statusMap(rep)
	if st["ghost.png"] != pipeline.StatusFailed || st["d.png"] != pipeline.StatusFailed {
		t.Fatalf("dangling targets must fail in lenient mode: %v", st)
	}
	r, _ := pack.Open(out)
	defer r.Close()
	if len(r.Manifest().Assets) != 1 {
		t.Fatalf("only ok.png should ship: %+v", r.Manifest().Assets)
	}

	// Strict build must report the locatable error.
	c3, _ := cache.Open(filepath.Join(t.TempDir(), "cache3"))
	_, err = pipeline.New(c3).Build(context.Background(), dir,
		filepath.Join(t.TempDir(), "x.pak"), config.Default(), config.Options{})
	if err == nil {
		t.Fatal("strict build must fail on dangling reference")
	}
}

func TestSidecarPathTraversalRejected(t *testing.T) {
	dir := t.TempDir()
	writePNG(t, filepath.Join(dir, "ok.png"), 4, 4, pngRed)
	writeSidecar(t, dir, "ok.png.refs.json",
		`{"kind":"refs","refs":[{"kind":"dependency","path":"../../etc/passwd"}]}`)
	c, _ := cache.Open(filepath.Join(t.TempDir(), "cache"))
	_, err := pipeline.New(c).Build(context.Background(), dir,
		filepath.Join(t.TempDir(), "x.pak"), config.Default(), config.Options{})
	if err == nil {
		t.Fatal("path traversal reference must be rejected")
	}
	_ = context.Background
}
