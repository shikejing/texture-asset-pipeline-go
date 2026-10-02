package pipeline

import (
	"encoding/json"
	"fmt"
	"path"
	"sort"
	"strings"
)

// sidecarSuffix marks reference manifests beside source assets:
// "tex.png.refs.json" describes references of "tex.png".
const sidecarSuffix = ".refs.json"

// sidecarDoc is the on-disk sidecar format.
//
// Either:
//
//	{"kind":"link","target":"other.png"}
//
// or:
//
//	{"kind":"refs","refs":[{"kind":"dependency","path":"shared/noise.png"}]}
//
// Paths are relative to the *directory* containing the sidecar and must
// stay inside the source tree (no ".." escapes).
type sidecarDoc struct {
	Kind   string       `json:"kind"`
	Target string       `json:"target,omitempty"`
	Refs   []sidecarRef `json:"refs,omitempty"`
}

type sidecarRef struct {
	Kind string `json:"kind"`
	Path string `json:"path"`
}

type parsedSidecar struct {
	kind   string // "link" or "refs"
	target string // logical path, for links
	refs   []string
}

// parseSidecar parses and validates a sidecar belonging to owner.
func parseSidecar(owner string, data []byte) (*parsedSidecar, error) {
	var doc sidecarDoc
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("invalid sidecar json: %w", err)
	}
	dir := path.Dir(owner)
	resolve := func(p string) (string, error) {
		if p == "" {
			return "", fmt.Errorf("empty reference path")
		}
		if strings.HasPrefix(p, "/") {
			return "", fmt.Errorf("absolute reference path %q not allowed", p)
		}
		joined := path.Clean(path.Join(dir, p))
		if joined == ".." || strings.HasPrefix(joined, "../") {
			return "", fmt.Errorf("reference %q escapes source tree", p)
		}
		return joined, nil
	}
	switch doc.Kind {
	case "link":
		if doc.Target == "" {
			return nil, fmt.Errorf("link sidecar requires target")
		}
		t, err := resolve(doc.Target)
		if err != nil {
			return nil, err
		}
		return &parsedSidecar{kind: "link", target: t}, nil
	case "refs", "":
		var refs []string
		for _, r := range doc.Refs {
			if r.Kind != "" && r.Kind != string(manifestRefKindDependency) {
				return nil, fmt.Errorf("unsupported reference kind %q", r.Kind)
			}
			t, err := resolve(r.Path)
			if err != nil {
				return nil, err
			}
			refs = append(refs, t)
		}
		sort.Strings(refs)
		// Sorted + unique: identical sidecars must produce identical refs.
		uniq := refs[:0]
		for _, r := range refs {
			if len(uniq) == 0 || uniq[len(uniq)-1] != r {
				uniq = append(uniq, r)
			}
		}
		return &parsedSidecar{kind: "refs", refs: uniq}, nil
	default:
		return nil, fmt.Errorf("unsupported sidecar kind %q", doc.Kind)
	}
}
