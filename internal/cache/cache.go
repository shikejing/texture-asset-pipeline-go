// Package cache implements a content-addressed, checksum-verified build cache.
package cache

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// ErrCorrupt is returned when a cache entry fails integrity verification.
var ErrCorrupt = errors.New("cache entry corrupt")

// ErrMiss is returned when a cache entry does not exist.
var ErrMiss = errors.New("cache miss")

// cacheMagic identifies a cache entry file.
var cacheMagic = [4]byte{'T', 'C', 'A', 'C'}

// cacheVersion is the cache entry format version.
const cacheVersion = 1

// Cache is a directory-backed content cache.
type Cache struct {
	dir string
}

// New returns a Cache rooted at dir, creating it if necessary.
func New(dir string) (*Cache, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	return &Cache{dir: dir}, nil
}

// Dir returns the cache root directory.
func (c *Cache) Dir() string { return c.dir }

// Path returns the on-disk path for a cache key.
func (c *Cache) Path(key string) string {
	return filepath.Join(c.dir, key+".cache")
}

// tmpPath returns a temp file path for atomic writes.
func (c *Cache) tmpPath(key string) string {
	return filepath.Join(c.dir, fmt.Sprintf(".tmp-%s-%d", key, os.Getpid()))
}

// Get reads and verifies a cache entry. A missing entry returns ErrMiss; a
// present but corrupt entry returns ErrCorrupt (and is removed).
func (c *Cache) Get(key string) ([]byte, error) {
	p := c.Path(key)
	b, err := os.ReadFile(p)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, ErrMiss
		}
		return nil, err
	}
	data, err := decodeEntry(b)
	if err != nil {
		// Corrupt entries must never be treated as valid.
		_ = os.Remove(p)
		return nil, ErrCorrupt
	}
	return data, nil
}

// Put atomically writes a cache entry. The entry is written to a temp file,
// fsynced, then renamed into place.
func (c *Cache) Put(key string, data []byte) error {
	entry := encodeEntry(key, data)
	tmp := c.tmpPath(key)
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.Write(entry); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	final := c.Path(key)
	if err := os.Rename(tmp, final); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

// Remove deletes a cache entry.
func (c *Cache) Remove(key string) error {
	err := os.Remove(c.Path(key))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}

// encodeEntry wraps data with a header and checksum.
// Layout: magic(4) version(1) keyLen(2) key dataLen(8) data sha256(32)
func encodeEntry(key string, data []byte) []byte {
	var buf bytes.Buffer
	buf.Write(cacheMagic[:])
	buf.WriteByte(cacheVersion)
	var kl [2]byte
	binary.LittleEndian.PutUint16(kl[:], uint16(len(key)))
	buf.Write(kl[:])
	buf.WriteString(key)
	var dl [8]byte
	binary.LittleEndian.PutUint64(dl[:], uint64(len(data)))
	buf.Write(dl[:])
	buf.Write(data)
	sum := sha256.Sum256(buf.Bytes())
	buf.Write(sum[:])
	return buf.Bytes()
}

// decodeEntry validates and unwraps a cache entry.
func decodeEntry(b []byte) ([]byte, error) {
	// Minimum: magic(4) + version(1) + keyLen(2) + dataLen(8) + sha256(32) = 47
	if len(b) < 47 {
		return nil, ErrCorrupt
	}
	if !bytes.Equal(b[0:4], cacheMagic[:]) {
		return nil, ErrCorrupt
	}
	if b[4] != cacheVersion {
		return nil, ErrCorrupt
	}
	keyLen := int(binary.LittleEndian.Uint16(b[5:7]))
	if 7+keyLen+8+32 > len(b) {
		return nil, ErrCorrupt
	}
	key := string(b[7 : 7+keyLen])
	_ = key
	dataOff := 7 + keyLen
	dataLen := int(binary.LittleEndian.Uint64(b[dataOff : dataOff+8]))
	payloadEnd := dataOff + 8 + dataLen
	if payloadEnd+32 != len(b) {
		return nil, ErrCorrupt
	}
	payload := b[:payloadEnd]
	sum := sha256.Sum256(payload)
	if !bytes.Equal(sum[:], b[payloadEnd:]) {
		return nil, ErrCorrupt
	}
	data := make([]byte, dataLen)
	copy(data, b[dataOff+8:payloadEnd])
	return data, nil
}
