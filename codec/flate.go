package codec

import (
	"bytes"
	"compress/zlib"
	"context"
	"fmt"
	"io"

	"github.com/shikejing/texture-asset-pipeline-go/config"
)

// flateCodec is the default deterministic zlib codec.
type flateCodec struct{}

func (c *flateCodec) Name() string { return "flate-v1" }

func zlibLevel(level config.CompressionLevel) (int, error) {
	switch {
	case level == 0:
		return zlib.DefaultCompression, nil
	case level >= 1 && level <= 9:
		return int(level), nil
	default:
		return 0, fmt.Errorf("codec: invalid level %d", level)
	}
}

func (c *flateCodec) Encode(ctx context.Context, data []byte, level config.CompressionLevel) ([]byte, error) {
	lvl, err := zlibLevel(level)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	buf.Write(frameMagic[:])
	w, err := zlib.NewWriterLevel(&buf, lvl)
	if err != nil {
		return nil, err
	}
	// Chunked writes let us honor cancellation/timeouts mid-asset so one
	// oversized asset cannot block the batch past its deadline.
	const chunk = 64 * 1024
	for len(data) > 0 {
		if err := ctx.Err(); err != nil {
			w.Close()
			return nil, err
		}
		n := chunk
		if n > len(data) {
			n = len(data)
		}
		if _, err := w.Write(data[:n]); err != nil {
			w.Close()
			return nil, err
		}
		data = data[n:]
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func (c *flateCodec) Decode(ctx context.Context, compressed []byte) ([]byte, error) {
	if len(compressed) < len(frameMagic) {
		return nil, fmt.Errorf("%w: payload too short", ErrCorruptPayload)
	}
	if compressed[0] != frameMagic[0] || compressed[1] != frameMagic[1] ||
		compressed[2] != frameMagic[2] || compressed[3] != frameMagic[3] {
		return nil, fmt.Errorf("%w: bad frame magic", ErrCorruptPayload)
	}
	r, err := zlib.NewReader(bytes.NewReader(compressed[len(frameMagic):]))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrCorruptPayload, err)
	}
	defer r.Close()
	var out bytes.Buffer
	if _, err := io.Copy(&out, r); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrCorruptPayload, err)
	}
	if err := r.Close(); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrCorruptPayload, err)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}
