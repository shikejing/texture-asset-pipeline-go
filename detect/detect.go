// Package detect identifies input formats and validates raw asset bytes.
//
// Identification is strictly content-based (magic numbers); file extensions
// are never trusted. Decoding is fully attempted so truncated or malformed
// files surface here rather than inside a pack.
package detect

import (
	"bytes"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
)

// Format is a recognized input texture format.
type Format string

const (
	// FormatPNG is image/png.
	FormatPNG Format = "png"
	// FormatJPEG is image/jpeg.
	FormatJPEG Format = "jpeg"
	// FormatPPM is the binary Netpbm P6 format.
	FormatPPM Format = "ppm"
)

// maxDimension guards against absurd dimension headers in corrupt files.
const maxDimension = 100000

// AssetInfo is the result of a successful identify/decode probe.
type AssetInfo struct {
	Format Format
	Width  int
	Height int
}

// StageError locates a failure within processing of a single asset.
type StageError struct {
	Path  string
	Stage string
	Err   error
}

func (e *StageError) Error() string {
	if e.Path == "" {
		return fmt.Sprintf("%s: %v", e.Stage, e.Err)
	}
	return fmt.Sprintf("%s: %s: %v", e.Stage, e.Path, e.Err)
}

func (e *StageError) Unwrap() error { return e.Err }

// NewStageError wraps err with asset path and processing stage.
func NewStageError(path, stage string, err error) *StageError {
	return &StageError{Path: path, Stage: stage, Err: err}
}

var (
	// ErrUnknownFormat means no supported format matched the input.
	ErrUnknownFormat = fmt.Errorf("detect: unknown or unsupported format")
	// ErrCorruptInput means the format matched but content is malformed.
	ErrCorruptInput = fmt.Errorf("detect: corrupt input")
)

var (
	pngMagic  = []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A}
	jpegMagic = []byte{0xFF, 0xD8, 0xFF}
)

// Identify detects the format from raw content bytes (never from extension).
func Identify(path string, data []byte) (Format, error) {
	switch {
	case bytes.HasPrefix(data, pngMagic):
		return FormatPNG, nil
	case bytes.HasPrefix(data, jpegMagic):
		return FormatJPEG, nil
	case isPPM(data):
		return FormatPPM, nil
	default:
		return "", NewStageError(path, "identify", ErrUnknownFormat)
	}
}

// Probe fully validates decodability and reports dimensions.
func Probe(path string, data []byte) (AssetInfo, error) {
	format, err := Identify(path, data)
	if err != nil {
		return AssetInfo{}, err
	}
	switch format {
	case FormatPNG, FormatJPEG:
		cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
		if err != nil {
			return AssetInfo{}, NewStageError(path, "decode", fmt.Errorf("%w: %v", ErrCorruptInput, err))
		}
		if cfg.Width <= 0 || cfg.Height <= 0 || cfg.Width > maxDimension || cfg.Height > maxDimension {
			return AssetInfo{}, NewStageError(path, "decode", fmt.Errorf("%w: illegal dimensions %dx%d", ErrCorruptInput, cfg.Width, cfg.Height))
		}
		img, _, err := image.Decode(bytes.NewReader(data))
		if err != nil {
			return AssetInfo{}, NewStageError(path, "decode", fmt.Errorf("%w: %v", ErrCorruptInput, err))
		}
		b := img.Bounds()
		return AssetInfo{Format: format, Width: b.Dx(), Height: b.Dy()}, nil
	case FormatPPM:
		w, h, _, payload, err := parsePPM(data)
		if err != nil {
			return AssetInfo{}, NewStageError(path, "decode", err)
		}
		_ = payload
		return AssetInfo{Format: FormatPPM, Width: w, Height: h}, nil
	default:
		return AssetInfo{}, NewStageError(path, "identify", ErrUnknownFormat)
	}
}
