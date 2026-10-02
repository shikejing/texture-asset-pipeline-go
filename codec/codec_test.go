package codec_test

import (
	"context"
	"errors"
	"testing"

	"github.com/shikejing/texture-asset-pipeline-go/codec"
	"github.com/shikejing/texture-asset-pipeline-go/config"
)

func TestEncodeDecodeRoundTrip(t *testing.T) {
	c, err := codec.Get(config.CodecFlate)
	if err != nil {
		t.Fatal(err)
	}
	in := bytes2(0, 4096)
	for i := range in {
		in[i] = byte(i * 7)
	}
	enc, err := c.Encode(context.Background(), in, 6)
	if err != nil {
		t.Fatal(err)
	}
	out, err := c.Decode(context.Background(), enc)
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != string(in) {
		t.Fatal("round trip mismatch")
	}
}

func TestEncodeIsDeterministicAcrossLevels(t *testing.T) {
	c, _ := codec.Get(config.CodecFlate)
	in := bytes2(0, 8192)
	a, _ := c.Encode(context.Background(), in, 9)
	b, _ := c.Encode(context.Background(), in, 9)
	if string(a) != string(b) {
		t.Fatal("same input+level must encode identically")
	}
	d, _ := c.Encode(context.Background(), in, 1)
	if string(a) == string(d) {
		// Not required to differ, but for this input they almost certainly do;
		// only assert determinism, skip if equal.
	}
	_ = d
}

func TestCorruptPayloadRejected(t *testing.T) {
	c, _ := codec.Get(config.CodecFlate)
	enc, err := c.Encode(context.Background(), []byte("payload"), 6)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Decode(context.Background(), enc[:len(enc)-2]); err == nil {
		t.Fatal("truncated payload accepted")
	}
	bad := append([]byte("XXXX"), enc[4:]...)
	if _, err := c.Decode(context.Background(), bad); !errors.Is(err, codec.ErrCorruptPayload) {
		t.Fatalf("want corrupt payload, got %v", err)
	}
}

func TestUnknownCodec(t *testing.T) {
	if _, err := codec.Get("lzma"); !errors.Is(err, codec.ErrUnsupportedCodec) {
		t.Fatalf("want unsupported, got %v", err)
	}
}

func bytes2(_ int, n int) []byte { return make([]byte, n) }
