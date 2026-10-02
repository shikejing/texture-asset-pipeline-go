package pipeline

import "testing"

func TestParseSidecarRefsSortedUnique(t *testing.T) {
	body := []byte(`{"kind":"refs","refs":[
		{"kind":"dependency","path":"z.png"},
		{"kind":"dependency","path":"a.png"},
		{"kind":"dependency","path":"a.png"},
		{"path":"m.png"}]}`)
	got, err := parseSidecar("dir/owner.png", body)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"dir/a.png", "dir/m.png", "dir/z.png"}
	if len(got.refs) != len(want) {
		t.Fatalf("refs=%v want %v", got.refs, want)
	}
	for i := range want {
		if got.refs[i] != want[i] {
			t.Fatalf("refs=%v want %v", got.refs, want)
		}
	}
}

func TestParseSidecarTraversalAndAbsolute(t *testing.T) {
	if _, err := parseSidecar("d/o.png", []byte(`{"kind":"refs","refs":[{"path":"../../x.png"}]}`)); err == nil {
		t.Fatal("parent traversal must fail")
	}
	if _, err := parseSidecar("d/o.png", []byte(`{"kind":"refs","refs":[{"path":"/abs/x.png"}]}`)); err == nil {
		t.Fatal("absolute path must fail")
	}
	if _, err := parseSidecar("d/o.png", []byte(`{"kind":"link","target":"x.png"}`)); err != nil {
		t.Fatalf("valid link failed: %v", err)
	}
	if _, err := parseSidecar("d/o.png", []byte(`{"kind":"weird"}`)); err == nil {
		t.Fatal("unknown kind must fail")
	}
	if _, err := parseSidecar("d/o.png", []byte(`{not json`)); err == nil {
		t.Fatal("invalid json must fail")
	}
}
