// Package config holds pipeline configuration and its deterministic serialization.
package config

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
)

// Config controls pipeline behavior. All fields that influence processing
// contribute to the cache key; purely operational fields (paths, workers)
// do not.
type Config struct {
	// Source is the root directory of source assets.
	Source string `json:"-"`
	// Output is the destination package file path.
	Output string `json:"-"`
	// CacheDir is the directory for the build cache.
	CacheDir string `json:"-"`

	// Workers is the number of concurrent processing workers.
	Workers int `json:"workers"`
	// MemoryLimit is an approximate cap on in-flight decoded bytes (0 = unlimited).
	MemoryLimit int64 `json:"memory_limit"`
	// DiskLimit is an approximate cap on in-flight temp bytes (0 = unlimited).
	DiskLimit int64 `json:"disk_limit"`
	// AssetTimeout is the per-asset processing timeout in milliseconds (0 = none).
	AssetTimeoutMS int64 `json:"asset_timeout_ms"`

	// CompressionLevel controls codec effort/quality (codec-specific).
	CompressionLevel int `json:"compression_level"`
	// MaxDimension rejects textures larger than this in either dimension (0 = unlimited).
	MaxDimension int `json:"max_dimension"`
	// GenerateMips toggles mipmap generation.
	GenerateMips bool `json:"generate_mips"`

	// AlgorithmVersion is bumped whenever the codec or packing semantics change
	// in a way that invalidates existing cache entries.
	AlgorithmVersion int `json:"algorithm_version"`
}

// Default returns a configuration with safe defaults.
func Default() Config {
	return Config{
		Workers:          4,
		MemoryLimit:      256 << 20,
		DiskLimit:        512 << 20,
		AssetTimeoutMS:   30000,
		CompressionLevel: 6,
		MaxDimension:     4096,
		GenerateMips:     false,
		AlgorithmVersion: 1,
	}
}

// Load reads configuration from r, applying defaults for unset fields.
func Load(r io.Reader) (Config, error) {
	c := Default()
	dec := json.NewDecoder(r)
	if err := dec.Decode(&c); err != nil {
	}
	return c, nil
}

// CacheKey returns a stable hash of the processing-relevant configuration.
// Operational fields (paths, workers, limits, timeouts) are excluded so that
// changing them does not invalidate the cache.
func (c Config) CacheKey() string {
	type key struct {
		CompressionLevel int  `json:"compression_level"`
		MaxDimension     int  `json:"max_dimension"`
		GenerateMips     bool `json:"generate_mips"`
		AlgorithmVersion int  `json:"algorithm_version"`
	}
	b, _ := json.Marshal(key{
		CompressionLevel: c.CompressionLevel,
		MaxDimension:     c.MaxDimension,
		GenerateMips:     c.GenerateMips,
		AlgorithmVersion: c.AlgorithmVersion,
	})
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
