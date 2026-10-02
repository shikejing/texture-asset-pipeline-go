// Package codec provides deterministic compression of validated textures.
//
// Payloads carry a small framing header so that corruption can never be
// confused for another encoding, followed by a zlib stream (Adler-32
// checksum). Go's zlib encoder at a fixed level is deterministic: equal
// input and level always yield equal bytes regardless of machine or
// concurrency, and it never reads timestamps or environment.
package codec

import (
	"bytes"
	"context"
	"errors"
	"fmt"

	"github.com/shikejing/texture-asset-pipeline-go/config"
)

// frameMagic identifies codec payloads: "FTX1".
var frameMagic = [4]byte{'F', 'T', 'X', '1'}

// Codec compresses raw, validated asset bytes.
type Codec interface {
	// Name is the stable codec identifier stored in the manifest.
	Name() string
	// Encode deterministically compresses data. Equal input+level must
	// produce byte-identical output regardless of machine or concurrency.
	Encode(ctx context.Context, data []byte, level config.CompressionLevel) ([]byte, error)
	// Decode reverses Encode and verifies the payload is well-formed.
	Decode(ctx context.Context, compressed []byte) ([]byte, error)
}

var (
	// ErrUnsupportedCodec is returned for unknown codec names.
	ErrUnsupportedCodec = errors.New("codec: unsupported codec")
	// ErrCorruptPayload means compressed bytes fail framing/checksum checks.
	ErrCorruptPayload = errors.New("codec: corrupt payload")
)

// Get resolves a codec from configuration.
func Get(name config.Codec) (Codec, error) {
	switch name {
	case config.CodecFlate:
		return &flateCodec{}, nil
	default:
		return nil, fmt.Errorf("%w: %q", ErrUnsupportedCodec, name)
	}
}

// Names returns the supported codec identifiers.
func Names() []config.Codec {
	return []config.Codec{config.CodecFlate}
}

// ensure frame is recognized.
var _ = bytes.MinRead
