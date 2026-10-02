// Package asset defines the asset model and processing results.
package asset

import "time"

// Kind classifies a source asset.
type Kind string

const (
	KindTexture   Kind = "texture"
	KindDescriptor Kind = "descriptor"
	KindUnknown   Kind = "unknown"
)

// Asset is a discovered source file.
type Asset struct {
	// RelPath is the path relative to the source root, using forward slashes.
	RelPath string
	// AbsPath is the absolute path on disk.
	AbsPath string
	// Size in bytes.
	Size int64
	// ModTime is the file modification time (used for diagnostics only).
	ModTime time.Time
	// ContentHash is the sha256 of the file bytes.
	ContentHash string
	// Kind classifies the asset.
	Kind Kind
}

// ResultStatus describes how an asset was resolved.
type ResultStatus string

const (
	StatusHit     ResultStatus = "hit"
	StatusMiss    ResultStatus = "miss"
	StatusError   ResultStatus = "error"
	StatusSkipped ResultStatus = "skipped"
)

// Result is the outcome of processing one asset.
type Result struct {
	Asset  Asset
	Status ResultStatus
	// BlobHash is the content hash of the processed blob (dedup key).
	BlobHash string
	// BlobSize is the processed blob size in bytes.
	BlobSize int64
	// Err is non-nil when Status == StatusError.
	Err error
	// Duration is the wall time spent processing this asset.
	Duration time.Duration
}

// IsTexture reports whether the asset is a texture source.
func (a Asset) IsTexture() bool { return a.Kind == KindTexture }

// IsDescriptor reports whether the asset is a descriptor source.
func (a Asset) IsDescriptor() bool { return a.Kind == KindDescriptor }
