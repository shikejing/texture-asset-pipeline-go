// Package pack writes and reads the TAPKG resource package and its manifest.
package pack

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
)

// FormatVersion is the package container format version.
const FormatVersion = 1

// MinReaderVersion is the oldest package format version this reader accepts.
const MinReaderVersion = 1

// MaxReaderVersion is the newest package format version this reader accepts.
const MaxReaderVersion = FormatVersion

// Magic identifies a TAPKG package file.
var Magic = [5]byte{'T', 'A', 'P', 'K', 'G'}

// Fixed header size: magic(5) + format(4) + minReader(4) + codec(4) + manifestLen(8).
const headerSize = 5 + 4 + 4 + 4 + 8

// Errors returned by the package reader/writer.
var (
	ErrTooOld    = errors.New("package format version too old")
	ErrTooNew    = errors.New("package format version too new")
	ErrCorrupt   = errors.New("package corrupt")
	ErrNotFound  = errors.New("blob not found")
)

// AssetEntry describes one source asset in the manifest.
type AssetEntry struct {
	RelPath     string `json:"rel_path"`
	Kind        string `json:"kind"`
	ContentHash string `json:"content_hash"`
	BlobHash    string `json:"blob_hash"`
	BlobSize    int64  `json:"blob_size"`
	HasAlpha    bool   `json:"has_alpha,omitempty"`
	Width       int    `json:"width,omitempty"`
	Height      int    `json:"height,omitempty"`
}

// BlobEntry locates a deduped content blob within the package.
type BlobEntry struct {
	Hash   string `json:"hash"`
	Offset int64  `json:"offset"`
	Size   int64  `json:"size"`
}

// Reference records a resolved reference from one asset to a content blob.
type Reference struct {
	FromAsset string `json:"from_asset"`
	Field     string `json:"field"`
	ToHash    string `json:"to_hash"`
	ToPath    string `json:"to_path"`
}

// Manifest is the package manifest.
type Manifest struct {
	Magic            string       `json:"magic"`
	FormatVersion    int          `json:"format_version"`
	MinReaderVersion int          `json:"min_reader_version"`
	CodecVersion     int          `json:"codec_version"`
	AssetCount       int          `json:"asset_count"`
	BlobCount        int          `json:"blob_count"`
	Assets           []AssetEntry `json:"assets"`
	Blobs            []BlobEntry  `json:"blobs"`
	References       []Reference  `json:"references"`
}

// Writer incrementally builds a package.
type Writer struct {
	path string
}

// NewWriter returns a Writer that will produce a package at path.
func NewWriter(path string) *Writer {
	return &Writer{path: path}
}

// Build writes the package deterministically from the given manifest and blob
// payloads. Blobs are sorted by hash before emission, so the output depends
// only on the set of blobs and the manifest content.
func (w *Writer) Build(m *Manifest, blobs map[string][]byte) error {
	// Sort blob hashes for deterministic layout.
	hashes := make([]string, 0, len(blobs))
	for h := range blobs {
		hashes = append(hashes, h)
	}
	sort.Strings(hashes)

	// Build blob entries with absolute file offsets.
	// Header size is fixed; manifest length is determined after marshaling.
	// We marshal the manifest with placeholder offsets first to find its size,
	// then recompute offsets.
	blobEntries := make([]BlobEntry, 0, len(hashes))
	var blobData bytes.Buffer
	for _, h := range hashes {
		data := blobs[h]
		blobEntries = append(blobEntries, BlobEntry{
			Hash:   h,
			Offset: 0, // filled below
			Size:   int64(len(data)),
		})
		blobData.Write(data)
	}

	// Marshal manifest to determine its serialized length. We temporarily set
	// offsets relative to blob data start; after marshaling we know the manifest
	// size and can compute absolute offsets.
	m.Blobs = blobEntries
	m.BlobCount = len(blobEntries)
	m.AssetCount = len(m.Assets)
	if m.Magic == "" {
		m.Magic = string(Magic[:])
	}
	if m.FormatVersion == 0 {
		m.FormatVersion = FormatVersion
	}
	if m.MinReaderVersion == 0 {
		m.MinReaderVersion = MinReaderVersion
	}

	// Marshal manifest to determine its serialized length. The blob offsets
	// depend on the manifest length (offsets are absolute file positions), and
	// the manifest length depends on the offset digit count, so iterate until
	// the length stabilizes. In practice this converges in one or two passes.
	var manifestBytes []byte
	for {
		b, err := json.Marshal(m)
		if err != nil {
			return err
		}
		blobDataStart := int64(headerSize + len(b))
		for i := range m.Blobs {
			if i == 0 {
				m.Blobs[i].Offset = blobDataStart
			} else {
				m.Blobs[i].Offset = m.Blobs[i-1].Offset + m.Blobs[i-1].Size
			}
		}
		manifestBytes = b
		// Re-marshal with the computed offsets; if the length is unchanged,
		// the offsets are stable.
		b2, err := json.Marshal(m)
		if err != nil {
			return err
		}
		if len(b2) == len(b) {
			manifestBytes = b2
			break
		}
	}

	var buf bytes.Buffer
	buf.Write(Magic[:])
	var hv [12]byte
	binary.LittleEndian.PutUint32(hv[0:4], uint32(m.FormatVersion))
	binary.LittleEndian.PutUint32(hv[4:8], uint32(m.MinReaderVersion))
	binary.LittleEndian.PutUint32(hv[8:12], uint32(m.CodecVersion))
	buf.Write(hv[:])
	var ml [8]byte
	binary.LittleEndian.PutUint64(ml[:], uint64(len(manifestBytes)))
	buf.Write(ml[:])
	buf.Write(manifestBytes)
	buf.Write(blobData.Bytes())

	sum := sha256.Sum256(buf.Bytes())
	buf.Write(sum[:])

	// Atomic write: temp file + fsync + rename.
	if err := os.MkdirAll(filepath.Dir(w.path), 0o755); err != nil {
		return err
	}
	tmp := w.path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.Write(buf.Bytes()); err != nil {
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
	return os.Rename(tmp, w.path)
}

// Reader reads and verifies a package.
type Reader struct {
	path string
}

// NewReader returns a Reader for the package at path.
func NewReader(path string) *Reader {
	return &Reader{path: path}
}

// readAll reads the whole file.
func (r *Reader) readAll() ([]byte, error) {
	b, err := os.ReadFile(r.path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("%w: %s", ErrNotFound, r.path)
		}
		return nil, err
	}
	return b, nil
}

// parseHeader validates the header and returns the manifest bytes and version.
func parseHeader(b []byte) (formatVersion, minReader, codec int, manifestBytes []byte, err error) {
	if len(b) < headerSize+32 {
		return 0, 0, 0, nil, ErrCorrupt
	}
	if !bytes.Equal(b[0:5], Magic[:]) {
		return 0, 0, 0, nil, ErrCorrupt
	}
	formatVersion = int(binary.LittleEndian.Uint32(b[5:9]))
	minReader = int(binary.LittleEndian.Uint32(b[9:13]))
	codec = int(binary.LittleEndian.Uint32(b[13:17]))
	manifestLen := int(binary.LittleEndian.Uint64(b[17:25]))
	if headerSize+manifestLen+32 > len(b) {
		return 0, 0, 0, nil, ErrCorrupt
	}
	manifestBytes = b[headerSize : headerSize+manifestLen]
	return formatVersion, minReader, codec, manifestBytes, nil
}

// checkVersion enforces the supported reader range.
func checkVersion(formatVersion, minReader int) error {
	if formatVersion < MinReaderVersion || minReader > MaxReaderVersion {
		return ErrTooOld
	}
	if formatVersion > MaxReaderVersion {
		return ErrTooNew
	}
	return nil
}

// ReadManifest loads and verifies the package manifest.
func (r *Reader) ReadManifest() (*Manifest, error) {
	b, err := r.readAll()
	if err != nil {
		return nil, err
	}
	formatVersion, minReader, codec, manifestBytes, err := parseHeader(b)
	if err != nil {
		return nil, err
	}
	if err := checkVersion(formatVersion, minReader); err != nil {
		return nil, err
	}
	// Verify footer hash over everything except the footer.
	payload := b[:len(b)-32]
	sum := sha256.Sum256(payload)
	if !bytes.Equal(sum[:], b[len(b)-32:]) {
		return nil, ErrCorrupt
	}
	var m Manifest
	if err := json.Unmarshal(manifestBytes, &m); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrCorrupt, err)
	}
	m.FormatVersion = formatVersion
	m.MinReaderVersion = minReader
	m.CodecVersion = codec
	return &m, nil
}

// ReadBlob returns the bytes for a content hash.
func (r *Reader) ReadBlob(hash string) ([]byte, error) {
	b, err := r.readAll()
	if err != nil {
		return nil, err
	}
	formatVersion, minReader, _, manifestBytes, err := parseHeader(b)
	if err != nil {
		return nil, err
	}
	if err := checkVersion(formatVersion, minReader); err != nil {
		return nil, err
	}
	payload := b[:len(b)-32]
	sum := sha256.Sum256(payload)
	if !bytes.Equal(sum[:], b[len(b)-32:]) {
		return nil, ErrCorrupt
	}
	var m Manifest
	if err := json.Unmarshal(manifestBytes, &m); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrCorrupt, err)
	}
	for _, bl := range m.Blobs {
		if bl.Hash == hash {
			start := bl.Offset
			end := start + bl.Size
			if start < 0 || end > int64(len(b)-32) {
				return nil, ErrCorrupt
			}
			out := make([]byte, bl.Size)
			copy(out, b[start:end])
			return out, nil
		}
	}
	return nil, fmt.Errorf("%w: %s", ErrNotFound, hash)
}

// Verify checks the package footer hash.
func (r *Reader) Verify() error {
	b, err := r.readAll()
	if err != nil {
		return err
	}
	formatVersion, minReader, _, _, err := parseHeader(b)
	if err != nil {
		return err
	}
	if err := checkVersion(formatVersion, minReader); err != nil {
		return err
	}
	payload := b[:len(b)-32]
	sum := sha256.Sum256(payload)
	if !bytes.Equal(sum[:], b[len(b)-32:]) {
		return ErrCorrupt
	}
	return nil
}
