// Package pack writes and reads deterministic texture package (.pak) files.
//
// Fixed header layout, all integers little-endian (total 84 bytes):
//
//	 0..4   "MTPAK" magic
//	 5      uint8  package version (= PackVersion)
//	 6      uint8  reserved (zero)
//	 8..9   uint16 manifest format version
//	12..15  uint32 algorithm version
//	16..19  uint32 manifest JSON length
//	20..51  [32]byte SHA-256 of the manifest JSON
//	52..83  [32]byte SHA-256 of the whole file with this field zeroed
//
// Followed by:
//
//	manifest JSON (canonical, see package manifest)
//	uint32 blob count
//	per blob: [32]byte payload SHA-256, uint64 payload length
//	payloads back-to-back in manifest blob order
//
// Determinism: no timestamps, no random padding, no map iteration; payload
// order is the manifest's (sorted) blob order.
package pack

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/shikejing/texture-asset-pipeline-go/manifest"
)

// Magic is the package magic number "MTPAK".
var Magic = [5]byte{'M', 'T', 'P', 'A', 'K'}

// PackVersion is the package format version produced by this builder.
const PackVersion uint8 = 1

// MinPackVersion is the oldest package version a current loader accepts.
const MinPackVersion uint8 = 1

const (
	headerSize = 84

	offMagic        = 0
	offPackVer      = 5
	offManifestVer  = 8
	offAlgoVer      = 12
	offManifestLen  = 16
	offManifestHash = 20
	offFileHash     = 52
)

// Entry is one data blob placed into a package.
type Entry struct {
	ID   string
	Data []byte
}

var (
	// ErrBadMagic means the file is not an MTPAK package.
	ErrBadMagic = errors.New("pack: bad magic")
	// ErrBadHeader means structural header fields are invalid.
	ErrBadHeader = errors.New("pack: malformed header")
	// ErrDigest means a checksum (file, manifest or blob) did not verify.
	ErrDigest = errors.New("pack: integrity check failed")
	// ErrBlobCount means the blob table disagrees with the manifest.
	ErrBlobCount = errors.New("pack: blob count mismatch")
	// ErrBlobOrder means payload order does not follow the manifest.
	ErrBlobOrder = errors.New("pack: blob order does not match manifest")
)

// Write serializes a package deterministically.
// The file is written atomically: a reader never observes a partial file.
func Write(path string, m *manifest.Manifest, blobs []Entry) error {
	var buf bytes.Buffer
	if err := WriteTo(&buf, m, blobs); err != nil {
		return err
	}
	return atomicWriteFile(path, buf.Bytes())
}

// WriteTo is the streaming variant of Write.
func WriteTo(w io.Writer, m *manifest.Manifest, blobs []Entry) error {
	if len(blobs) != len(m.Blobs) {
		return fmt.Errorf("%w: have %d payloads, manifest lists %d",
			ErrBlobCount, len(blobs), len(m.Blobs))
	}
	manifestBytes, err := manifest.Marshal(m)
	if err != nil {
		return err
	}
	manifestSum := sha256.Sum256(manifestBytes)

	out := make([]byte, 0, headerSize+len(manifestBytes))
	header := make([]byte, headerSize)
	copy(header[offMagic:offMagic+5], Magic[:])
	header[offPackVer] = PackVersion
	binary.LittleEndian.PutUint16(header[offManifestVer:offManifestVer+2], m.FormatVersion)
	binary.LittleEndian.PutUint32(header[offAlgoVer:offAlgoVer+4], uint32(m.AlgorithmVersion))
	binary.LittleEndian.PutUint32(header[offManifestLen:offManifestLen+4], uint32(len(manifestBytes)))
	copy(header[offManifestHash:offManifestHash+32], manifestSum[:])

	body := bytes.NewBuffer(header)
	body.Write(manifestBytes)
	if err := binary.Write(body, binary.LittleEndian, uint32(len(blobs))); err != nil {
		return err
	}
	for i, b := range blobs {
		if b.ID != m.Blobs[i].ID {
			return fmt.Errorf("%w: slot %d: entry %q vs manifest %q",
				ErrBlobOrder, i, b.ID, m.Blobs[i].ID)
		}
		sum := sha256.Sum256(b.Data)
		body.Write(sum[:])
		if err := binary.Write(body, binary.LittleEndian, uint64(len(b.Data))); err != nil {
			return err
		}
	}
	for _, b := range blobs {
		body.Write(b.Data)
	}
	out = body.Bytes()

	// Whole-file digest covers every byte except the digest field itself.
	var zero [32]byte
	copy(out[offFileHash:offFileHash+32], zero[:])
	digest := sha256.Sum256(out)
	copy(out[offFileHash:offFileHash+32], digest[:])

	_, err = w.Write(out)
	return err
}

// Reader provides random access to a loaded package.
type Reader struct {
	manifest *manifest.Manifest
	blobs    map[string][]byte
}

// Open reads and validates a package and its manifest.
func Open(path string) (*Reader, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return Parse(data)
}

// Parse validates an in-memory package and returns a Reader.
func Parse(data []byte) (*Reader, error) {
	bad := func(format string, args ...any) error {
		return fmt.Errorf("%w: %s", ErrBadHeader, fmt.Sprintf(format, args...))
	}
	if len(data) < headerSize || !bytes.Equal(data[0:5], Magic[:]) {
		return nil, ErrBadMagic
	}
	pver := data[offPackVer]
	if pver < MinPackVersion {
		return nil, fmt.Errorf("pack: unsupported package version %d", pver)
	}
	mver := binary.LittleEndian.Uint16(data[offManifestVer : offManifestVer+2])
	algVer := binary.LittleEndian.Uint32(data[offAlgoVer : offAlgoVer+4])
	manifestLen := int(binary.LittleEndian.Uint32(data[offManifestLen : offManifestLen+4]))
	var manifestSum [32]byte
	copy(manifestSum[:], data[offManifestHash:offManifestHash+32])

	// Verify whole-file integrity first (digest field excluded).
	var want [32]byte
	copy(want[:], data[offFileHash:offFileHash+32])
	without := bytes.Clone(data)
	for i := offFileHash; i < offFileHash+32; i++ {
		without[i] = 0
	}
	if sha256.Sum256(without) != want {
		return nil, ErrDigest
	}

	pos := headerSize
	if manifestLen < 0 || pos+manifestLen > len(data) {
		return nil, bad("manifest overruns file")
	}
	mb := data[pos : pos+manifestLen]
	if sha256.Sum256(mb) != manifestSum {
		return nil, fmt.Errorf("pack: manifest %w", ErrDigest)
	}
	m, err := manifest.Unmarshal(mb)
	if err != nil {
		return nil, err
	}
	if m.FormatVersion != mver || uint32(m.AlgorithmVersion) != algVer {
		return nil, bad("header/manifest version mismatch")
	}
	if err := manifest.CheckCompatibility(m, m.FormatVersion); err != nil {
		return nil, err
	}
	pos += manifestLen

	if pos+4 > len(data) {
		return nil, bad("missing blob table")
	}
	count := binary.LittleEndian.Uint32(data[pos : pos+4])
	pos += 4
	if int(count) != len(m.Blobs) {
		return nil, fmt.Errorf("%w: table %d, manifest %d", ErrBlobCount, count, len(m.Blobs))
	}
	type loc struct {
		id string
		n  int
	}
	locs := make([]loc, 0, count)
	for i := uint32(0); i < count; i++ {
		if pos+40 > len(data) {
			return nil, bad("truncated blob table")
		}
		var sum [32]byte
		copy(sum[:], data[pos:pos+32])
		pos += 32
		n := int(binary.LittleEndian.Uint64(data[pos : pos+8]))
		pos += 8
		if m.Blobs[i].Hash != hex.EncodeToString(sum[:]) {
			return nil, fmt.Errorf("pack: blob %d hash mismatch with manifest", i)
		}
		locs = append(locs, loc{id: m.Blobs[i].ID, n: n})
	}
	r := &Reader{manifest: m, blobs: make(map[string][]byte, len(m.Blobs))}
	for i := range locs {
		l := locs[i]
		if l.n < 0 || pos+l.n > len(data) {
			return nil, bad("payload overruns file")
		}
		payload := data[pos : pos+l.n]
		sum := sha256.Sum256(payload)
		if m.Blobs[i].Hash != hex.EncodeToString(sum[:]) {
			return nil, fmt.Errorf("pack: blob %q %w", l.id, ErrDigest)
		}
		if m.Blobs[i].Size != int64(l.n) {
			return nil, bad("blob %q size mismatch", l.id)
		}
		r.blobs[l.id] = payload
		pos += l.n
	}
	if pos != len(data) {
		return nil, bad("%d trailing bytes", len(data)-pos)
	}
	return r, nil
}

// Manifest returns the embedded manifest.
func (r *Reader) Manifest() *manifest.Manifest { return r.manifest }

// Blob returns one payload by content id.
func (r *Reader) Blob(id string) ([]byte, error) {
	b, ok := r.blobs[id]
	if !ok {
		return nil, fmt.Errorf("pack: unknown blob %q", id)
	}
	return b, nil
}

// Close releases reader resources.
func (r *Reader) Close() error { return nil }

func atomicWriteFile(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".pak-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			os.Remove(tmpName)
		}
	}()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	// CreateTemp starts at 0600; publish packs as regular readable files.
	if err := os.Chmod(tmpName, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return err
	}
	cleanup = false
	return nil
}
