// Package config defines the build configuration for the texture pipeline.
package config

import (
	"context"
	"errors"
	"runtime"
)

// AlgorithmVersion identifies the build algorithm. Any change to processing
// semantics bumps this value, invalidating all previous cache entries.
const AlgorithmVersion = 1

// Codec selects the compression codec used for texture payloads.
type Codec string

// Supported codecs.
const (
	CodecFlate Codec = "flate"
)

// CompressionLevel is a codec-independent effort/quality preset.
// Zero maps to the codec default; supported range is 1..9.
type CompressionLevel int

// OnError controls how individual asset failures affect the build.
type OnError string

const (
	// OnErrorStrict (default) makes the first bad asset fail the whole build.
	OnErrorStrict OnError = "strict"
	// OnErrorLenient skips bad assets, recording diagnostics instead.
	OnErrorLenient OnError = "lenient"
)

// Config is the complete, content-stable configuration of a build.
// Only values that affect output belong here; operational knobs
// (timeouts, cache paths, parallelism) live in Options.
type Config struct {
	Codec            Codec
	CompressionLevel CompressionLevel
	// OnError is Strict (default: fail the build) or Lenient
	// (record diagnostics, skip the asset). Lenient mode diagnostics
	// are reported alongside the build but never written into the pack,
	// so they cannot affect output determinism.
	OnError OnError
}

// Options are operational knobs that never affect the output bytes.
type Options struct {
	// Jobs is the maximum number of concurrently processed assets.
	// Values <= 0 mean GOMAXPROCS.
	Jobs int
	// CacheDir is the build cache root. Empty disables caching.
	CacheDir string
	// MaxCacheBytes bounds on-disk cache usage; <= 0 means unlimited.
	MaxCacheBytes int64
	// PerAssetTimeoutMS bounds processing of a single asset; <= 0 = none.
	PerAssetTimeoutMS int
}

var (
	errBadCodec   = errors.New("config: unknown codec")
	errBadLevel   = errors.New("config: compression level must be 0 or between 1 and 9")
	errBadOnError = errors.New("config: on_error must be strict or lenient")
)

// Validate returns an error if the configuration is unusable.
func (c Config) Validate() error {
	switch c.Codec {
	case CodecFlate:
	default:
		return errBadCodec
	}
	if c.CompressionLevel < 0 || c.CompressionLevel > 9 {
		return errBadLevel
	}
	switch c.OnError {
	case "", OnErrorStrict, OnErrorLenient:
	default:
		return errBadOnError
	}
	return nil
}

// Default returns the default build configuration.
func Default() Config {
	return Config{
		Codec:            CodecFlate,
		CompressionLevel: 6,
		OnError:          OnErrorStrict,
	}
}

// DefaultOptions returns operational defaults derived from ctx (unused).
func DefaultOptions(_ context.Context) Options {
	return Options{
		Jobs:              runtime.GOMAXPROCS(0),
		CacheDir:          "",
		MaxCacheBytes:     0,
		PerAssetTimeoutMS: 0,
	}
}
