package cache

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"github.com/shikejing/texture-asset-pipeline-go/config"
)

// fingerprintInput is the exact set of output-relevant inputs. Operational
// options (jobs, timeouts, cache path) are intentionally absent.
type fingerprintInput struct {
	AlgorithmVersion int    `json:"algorithm_version"`
	CacheFormat      int    `json:"cache_format"`
	Codec            string `json:"codec"`
	CompressionLevel int    `json:"compression_level"`
}

// ConfigFingerprint returns a stable hex digest of every output-relevant
// configuration field, including the algorithm version. Any change here
// forces all cache entries to miss.
func ConfigFingerprint(cfg config.Config) string {
	in := fingerprintInput{
		AlgorithmVersion: config.AlgorithmVersion,
		CacheFormat:      CacheFormatVersion,
		Codec:            string(cfg.Codec),
		CompressionLevel: int(cfg.CompressionLevel),
	}
	// Canonical JSON encoding (struct, fixed field order).
	b, err := json.Marshal(&in)
	if err != nil {
		// fingerprintInput is fully marshalable; panic indicates a bug.
		panic(fmt.Sprintf("cache: fingerprint marshal: %v", err))
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
