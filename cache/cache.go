// Package cache implements a content-addressed, integrity-checked build cache.
//
// Entries are keyed by SHA-256 of (raw content hash, full configuration
// fingerprint). Each entry is framed with explicit lengths and stores the
// SHA-256 of both raw and encoded payloads, so truncation, bit rot and
// partial writes (a lingering temp file) can never be read back as valid.
// Writes are temp-file + fsync + rename, hence atomic.
//
// A global stamp file pins the on-disk cache format version; an algorithm or
// format bump changes the stamp and invalidates the entire cache at open.
package cache

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// CacheFormatVersion is the on-disk cache layout version. Bump together with
// config.AlgorithmVersion when processing semantics change.
const CacheFormatVersion = 1

const (
	versionFile = "VERSION"
	resultsDir  = "results"
	entryMagic  = "TXC1"
)

// entryMeta is the JSON header stored inside each cache entry.
type entryMeta struct {
	Path        string `json:"path"`
	Type        string `json:"type"`
	RawHash     string `json:"raw_hash"`
	EncodedHash string `json:"encoded_hash"`
	RawSize     int64  `json:"raw_size"`
	EncodedSize int64  `json:"encoded_size"`
}

// Result is a cached per-asset processing outcome.
type Result struct {
	// Path is the source-relative logical path of the asset.
	Path string
	Type string
	// Encoded is the compressed payload.
	Encoded []byte
	// Raw is the original content (kept for reference resolution/hashing).
	Raw []byte
	// RawHash / EncodedHash are hex SHA-256 digests.
	RawHash     string
	EncodedHash string
	RawSize     int64
	EncodedSize int64
}

// Cache stores and retrieves per-asset results keyed purely by
// input content hash plus the full configuration fingerprint.
type Cache struct {
	dir string
}

var (
	// ErrMiss means no valid cache entry exists for the key.
	ErrMiss = errors.New("cache: miss")
	// ErrCorrupt means an entry exists but failed integrity verification;
	// it has been purged, so the caller may safely recompute.
	ErrCorrupt = errors.New("cache: corrupt entry")
)

// Open opens (or initializes) a cache rooted at dir. The global version
// stamp is checked; a mismatch invalidates the whole cache.
func Open(dir string) (*Cache, error) {
	if dir == "" {
		return nil, errors.New("cache: empty directory")
	}
	results := filepath.Join(dir, resultsDir)
	if err := os.MkdirAll(results, 0o755); err != nil {
		return nil, err
	}
	// Sweep atomic-write temp files left behind by a crashed/interrupted run.
	// They can never be read as entries (they are not key-named), but they
	// would otherwise leak disk space forever.
	if err := sweepTempFiles(results); err != nil {
		return nil, err
	}
	stamp := fmt.Sprintf("%d\n", CacheFormatVersion)
	if existing, err := os.ReadFile(filepath.Join(dir, versionFile)); err == nil {
		if string(existing) != stamp {
			// Whole-cache invalidation: old layout or algorithm.
			if err := os.RemoveAll(filepath.Join(dir, resultsDir)); err != nil {
				return nil, err
			}
			if err := os.MkdirAll(filepath.Join(dir, resultsDir), 0o755); err != nil {
				return nil, err
			}
			if err := writeStamp(dir, stamp); err != nil {
				return nil, err
			}
		}
	} else if errors.Is(err, os.ErrNotExist) {
		if err := writeStamp(dir, stamp); err != nil {
			return nil, err
		}
	} else {
		return nil, err
	}
	return &Cache{dir: dir}, nil
}

func writeStamp(dir, stamp string) error {
	return atomicWriteFile(filepath.Join(dir, versionFile), []byte(stamp))
}

// Close releases cache resources.
func (c *Cache) Close() error { return nil }

// HashBytes returns the hex SHA-256 digest of data.
func HashBytes(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// Key computes the cache key for a raw content hash and config fingerprint.
// The key is content-derived: identical inputs map to identical entries even
// across machines and runs.
func Key(rawHash string, configFingerprint string) string {
	h := sha256.New()
	io.WriteString(h, rawHash)
	io.WriteString(h, "|")
	io.WriteString(h, configFingerprint)
	return hex.EncodeToString(h.Sum(nil))
}

func (c *Cache) entryPath(key string) string {
	return filepath.Join(c.dir, resultsDir, key)
}

// Get returns a verified cached result. Corrupt entries are removed and
// ErrCorrupt is returned; missing entries return ErrMiss.
func (c *Cache) Get(ctx context.Context, key string) (*Result, error) {
	path := c.entryPath(key)
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ErrMiss
		}
		return nil, err
	}
	r, err := decodeEntry(data)
	if err != nil {
		// Purge anything that fails verification.
		_ = os.Remove(path)
		return nil, fmt.Errorf("%w: key %s: %v", ErrCorrupt, key[:8], err)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return r, nil
}

// Put atomically stores a result. Partial writes are never observable.
func (c *Cache) Put(ctx context.Context, key string, r *Result) error {
	data, err := encodeEntry(r)
	if err != nil {
		return err
	}
	if err := atomicWriteFile(c.entryPath(key), data); err != nil {
		return err
	}
	return ctx.Err()
}

// Purge removes a cache entry (e.g. after detecting corruption).
func (c *Cache) Purge(key string) error {
	err := os.Remove(c.entryPath(key))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

// Dir returns the cache root directory.
func (c *Cache) Dir() string { return c.dir }

func encodeEntry(r *Result) ([]byte, error) {
	if r.RawHash == "" {
		r.RawHash = HashBytes(r.Raw)
	}
	if r.EncodedHash == "" {
		r.EncodedHash = HashBytes(r.Encoded)
	}
	if r.RawSize == 0 {
		r.RawSize = int64(len(r.Raw))
	}
	if r.EncodedSize == 0 {
		r.EncodedSize = int64(len(r.Encoded))
	}
	meta := entryMeta{
		Path: r.Path, Type: r.Type,
		RawHash: r.RawHash, EncodedHash: r.EncodedHash,
		RawSize: r.RawSize, EncodedSize: r.EncodedSize,
	}
	metaJSON, err := json.Marshal(&meta)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	buf.WriteString(entryMagic)
	if err := binary.Write(&buf, binary.BigEndian, uint32(len(metaJSON))); err != nil {
		return nil, err
	}
	buf.Write(metaJSON)
	if err := binary.Write(&buf, binary.BigEndian, uint64(len(r.Raw))); err != nil {
		return nil, err
	}
	buf.Write(r.Raw)
	if err := binary.Write(&buf, binary.BigEndian, uint64(len(r.Encoded))); err != nil {
		return nil, err
	}
	buf.Write(r.Encoded)
	return buf.Bytes(), nil
}

func decodeEntry(data []byte) (*Result, error) {
	bad := func(format string, args ...any) error {
		return fmt.Errorf("%w: %s", ErrCorrupt, fmt.Sprintf(format, args...))
	}
	if len(data) < len(entryMagic)+4 {
		return nil, bad("entry too short")
	}
	if string(data[:len(entryMagic)]) != entryMagic {
		return nil, bad("bad magic")
	}
	pos := len(entryMagic)
	metaLen := int(binary.BigEndian.Uint32(data[pos : pos+4]))
	pos += 4
	if metaLen > len(data)-pos {
		return nil, bad("truncated meta")
	}
	var meta entryMeta
	if err := json.Unmarshal(data[pos:pos+metaLen], &meta); err != nil {
		return nil, bad("meta json: %v", err)
	}
	pos += metaLen
	readBlob := func() ([]byte, error) {
		if pos+8 > len(data) {
			return nil, bad("truncated length")
		}
		n := int(binary.BigEndian.Uint64(data[pos : pos+8]))
		pos += 8
		if n > len(data)-pos {
			return nil, bad("truncated payload")
		}
		b := data[pos : pos+n]
		pos += n
		return b, nil
	}
	raw, err := readBlob()
	if err != nil {
		return nil, err
	}
	encoded, err := readBlob()
	if err != nil {
		return nil, err
	}
	if pos != len(data) {
		return nil, bad("%d trailing bytes", len(data)-pos)
	}
	if meta.RawSize != int64(len(raw)) || meta.EncodedSize != int64(len(encoded)) {
		return nil, bad("size mismatch")
	}
	if got := HashBytes(raw); got != meta.RawHash {
		return nil, bad("raw hash mismatch")
	}
	if got := HashBytes(encoded); got != meta.EncodedHash {
		return nil, bad("encoded hash mismatch")
	}
	return &Result{
		Path: meta.Path, Type: meta.Type,
		Raw: raw, Encoded: encoded,
		RawHash: meta.RawHash, EncodedHash: meta.EncodedHash,
		RawSize: meta.RawSize, EncodedSize: meta.EncodedSize,
	}, nil
}

// atomicWriteFile writes data to path via a unique temp file in the same
// directory, fsyncs it and renames it into place.
func atomicWriteFile(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	var randBuf [16]byte
	if _, err := rand.Read(randBuf[:]); err != nil {
		return err
	}
	tmp := filepath.Join(dir, ".tmp-"+hex.EncodeToString(randBuf[:]))
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	cleanup := true
	defer func() {
		if cleanup {
			os.Remove(tmp)
		}
	}()
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	cleanup = false
	return syncDir(dir)
}

// sweepTempFiles removes leftover ".tmp-*" files from a cache directory.
func sweepTempFiles(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if len(name) >= 5 && name[:5] == ".tmp-" {
			if err := os.Remove(filepath.Join(dir, name)); err != nil && !os.IsNotExist(err) {
				return err
			}
		}
	}
	return nil
}
