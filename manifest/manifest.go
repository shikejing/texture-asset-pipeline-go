// Package manifest defines the on-disk resource manifest embedded in packs.
//
// The manifest is canonical JSON: fields appear in a fixed declaration
// order, HTML escaping is disabled and no maps are serialized, so output is
// byte-identical for equal content on every machine and Go version.
package manifest

import (
	"bytes"
	"encoding/json"
	"io"
)

// FormatVersion is the manifest format version produced by this builder.
const FormatVersion uint16 = 1

// MinSupportedVersion is the oldest manifest version a current loader accepts.
const MinSupportedVersion uint16 = 1

// AssetType classifies a packed asset.
type AssetType string

const (
	// TypeImage is a raster texture.
	TypeImage AssetType = "image"
	// TypeLink is a logical asset carrying only references (no own payload;
	// it aliases another asset's blob).
	TypeLink AssetType = "link"
)

// RefKind describes how an inter-asset reference is interpreted.
type RefKind string

const (
	// RefDependency is a generic hard reference to another packed asset.
	RefDependency RefKind = "dependency"
	// RefLink marks the alias edge from a link asset to its payload owner.
	RefLink RefKind = "link"
)

// Ref is a logical reference from one asset to another.
// Path is the referenced source-relative logical path; TargetBlob is filled
// in during the build once content hashing resolves the destination.
type Ref struct {
	Kind       RefKind `json:"kind"`
	Path       string  `json:"path"`
	TargetBlob string  `json:"target_blob,omitempty"`
}

// Asset describes one source asset in the manifest.
type Asset struct {
	Path string    `json:"path"`
	Type AssetType `json:"type"`
	// Blob references the deduplicated payload content id. Link assets
	// point at the blob of their target.
	Blob    string `json:"blob"`
	Size    int64  `json:"size"`
	RawHash string `json:"raw_hash"`
	Refs    []Ref  `json:"refs,omitempty"`
}

// Blob describes one deduplicated content payload in the pack.
type Blob struct {
	ID      string `json:"id"`
	Codec   string `json:"codec"`
	Size    int64  `json:"size"`
	RawSize int64  `json:"raw_size"`
	Hash    string `json:"hash"`
}

// Diagnostic records a skipped/failed asset in lenient mode. Diagnostics
// live outside the content manifest and are never embedded in a pack, so
// they cannot affect output determinism.
type Diagnostic struct {
	Path   string `json:"path"`
	Stage  string `json:"stage"`
	Reason string `json:"reason"`
}

// Manifest is the deterministic content description of a pack.
type Manifest struct {
	FormatVersion    uint16  `json:"format_version"`
	AlgorithmVersion int     `json:"algorithm_version"`
	Codec            string  `json:"codec"`
	Blobs            []Blob  `json:"blobs"`
	Assets           []Asset `json:"assets"`
}

// Marshal deterministically serializes a manifest to canonical JSON
// (compact, HTML escaping disabled, trailing newline).
func Marshal(m *Manifest) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(m); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// Unmarshal parses a manifest from JSON.
func Unmarshal(data []byte) (*Manifest, error) {
	var m Manifest
	dec := json.NewDecoder(bytes.NewReader(data))
	if err := dec.Decode(&m); err != nil {
		return nil, err
	}
	return &m, nil
}

// Encode deterministically serializes the manifest.
func Encode(w io.Writer, m *Manifest) error {
	b, err := Marshal(m)
	if err != nil {
		return err
	}
	_, err = w.Write(b)
	return err
}

// Decode parses a manifest.
func Decode(r io.Reader) (*Manifest, error) {
	b, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	return Unmarshal(b)
}
