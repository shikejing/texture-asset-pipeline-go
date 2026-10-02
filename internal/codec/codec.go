// Package codec decodes source images and compresses them into the
// deterministic TTEX texture format.
package codec

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"image/draw"
	_ "image/jpeg" // register JPEG
	_ "image/png"  // register PNG
	"io"
)

// FormatVersion is the TTEX container format version.
const FormatVersion = 1

// PixelFormat identifies the compressed pixel layout.
type PixelFormat uint8

const (
	PixelFormatBC1 PixelFormat = iota + 1
)

const (
	texMagic   = "TTEX"
	texFlagAlpha = 1 << 0
)

// Errors returned by the codec.
var (
	ErrBadMagic   = errors.New("codec: bad magic")
	ErrBadVersion = errors.New("codec: unsupported version")
	ErrCorrupt    = errors.New("codec: corrupt container")
	ErrDimensions = errors.New("codec: invalid dimensions")
)

// Texture is a decoded, validated image ready for compression.
type Texture struct {
	Width  int
	Height int
	Pix    []byte // RGBA, 4 bytes per pixel
	Stride int
}

// Processed is the result of compressing a texture.
type Processed struct {
	Width    int
	Height   int
	Format   PixelFormat
	HasAlpha bool
	// Data is the compressed payload.
	Data []byte
}

// Decode reads an image from r and returns a normalized RGBA texture.
func Decode(r io.Reader) (*Texture, error) {
	img, _, err := image.Decode(r)
	if err != nil {
		return nil, fmt.Errorf("decode image: %w", err)
	}
	b := img.Bounds()
	if b.Dx() <= 0 || b.Dy() <= 0 {
		return nil, ErrDimensions
	}
	nrgba := image.NewNRGBA(b)
	draw.Draw(nrgba, b, img, b.Min, draw.Src)
	return &Texture{
		Width:  b.Dx(),
		Height: b.Dy(),
		Pix:    nrgba.Pix,
		Stride: nrgba.Stride,
	}, nil
}

// Compress encodes a texture into BC1 blocks. The level parameter controls
// endpoint selection effort; higher levels may improve quality but the output
// is always deterministic for a given level.
func Compress(tex *Texture, level int) (*Processed, error) {
	if tex == nil || tex.Width <= 0 || tex.Height <= 0 || len(tex.Pix) < tex.Stride*tex.Height {
		return nil, ErrDimensions
	}
	hasAlpha := false
	for i := 3; i < len(tex.Pix); i += 4 {
		if tex.Pix[i] < 255 {
			hasAlpha = true
			break
		}
	}
	bw := (tex.Width + 3) / 4
	bh := (tex.Height + 3) / 4
	data := make([]byte, bw*bh*8)
	for by := 0; by < bh; by++ {
		for bx := 0; bx < bw; bx++ {
			block := compressBlock(tex, bx*4, by*4, level)
			copy(data[(by*bw+bx)*8:], block[:])
		}
	}
	return &Processed{
		Width:    tex.Width,
		Height:   tex.Height,
		Format:   PixelFormatBC1,
		HasAlpha: hasAlpha,
		Data:     data,
	}, nil
}

// rgb565 packs 8-bit channels into a 16-bit RGB565 value.
func rgb565(r, g, b uint8) uint16 {
	return (uint16(r>>3) << 11) | (uint16(g>>2) << 5) | uint16(b>>3)
}

// unpack565 expands a 16-bit RGB565 value to 8-bit channels.
func unpack565(c uint16) (r, g, b uint8) {
	r5 := (c >> 11) & 0x1F
	g6 := (c >> 5) & 0x3F
	b5 := c & 0x1F
	r = uint8((r5 << 3) | (r5 >> 2))
	g = uint8((g6 << 2) | (g6 >> 4))
	b = uint8((b5 << 3) | (b5 >> 2))
	return
}

// pixelAt returns the RGBA values for a pixel, with out-of-bounds pixels
// clamped to the edge.
func pixelAt(tex *Texture, x, y int) (r, g, b, a uint8) {
	if x >= tex.Width {
		x = tex.Width - 1
	}
	if y >= tex.Height {
		y = tex.Height - 1
	}
	i := y*tex.Stride + x*4
	return tex.Pix[i], tex.Pix[i+1], tex.Pix[i+2], tex.Pix[i+3]
}

// compressBlock encodes one 4x4 block into 8 bytes (BC1).
func compressBlock(tex *Texture, ox, oy, level int) [8]byte {
	var colors [16][4]uint8
	var alpha [16]bool
	anyAlpha := false
	for i := 0; i < 16; i++ {
		x := ox + i%4
		y := oy + i/4
		r, g, b, a := pixelAt(tex, x, y)
		colors[i] = [4]uint8{r, g, b, a}
		if a < 128 {
			alpha[i] = true
			anyAlpha = true
		}
	}

	// Bounding-box endpoints over opaque pixels.
	var minR, minG, minB uint8 = 255, 255, 255
	var maxR, maxG, maxB uint8 = 0, 0, 0
	opaqueCount := 0
	for i := 0; i < 16; i++ {
		if alpha[i] {
			continue
		}
		opaqueCount++
		r, g, b := colors[i][0], colors[i][1], colors[i][2]
		if r < minR {
			minR = r
		}
		if g < minG {
			minG = g
		}
		if b < minB {
			minB = b
		}
		if r > maxR {
			maxR = r
		}
		if g > maxG {
			maxG = g
		}
		if b > maxB {
			maxB = b
		}
	}
	if opaqueCount == 0 {
		// Fully transparent block: emit transparent black.
		return [8]byte{0, 0, 0, 0, 0xFF, 0xFF, 0xFF, 0xFF}
	}

	c0 := rgb565(maxR, maxG, maxB)
	c1 := rgb565(minR, minG, minB)

	var palette [4][3]uint8
	var useAlpha bool
	if anyAlpha {
		// 3-color mode: c0 <= c1 selects transparent.
		if c0 > c1 {
			c0, c1 = c1, c0
		}
		r0, g0, b0 := unpack565(c0)
		r1, g1, b1 := unpack565(c1)
		palette[0] = [3]uint8{r0, g0, b0}
		palette[1] = [3]uint8{r1, g1, b1}
		palette[2] = [3]uint8{
			uint8((uint16(r0) + uint16(r1)) / 2),
			uint8((uint16(g0) + uint16(g1)) / 2),
			uint8((uint16(b0) + uint16(b1)) / 2),
		}
		useAlpha = true
	} else {
		// 4-color mode: c0 > c1.
		if c0 < c1 {
			c0, c1 = c1, c0
		}
		r0, g0, b0 := unpack565(c0)
		r1, g1, b1 := unpack565(c1)
		palette[0] = [3]uint8{r0, g0, b0}
		palette[1] = [3]uint8{r1, g1, b1}
		palette[2] = [3]uint8{
			uint8((uint16(r0)*2 + uint16(r1)) / 3),
			uint8((uint16(g0)*2 + uint16(g1)) / 3),
			uint8((uint16(b0)*2 + uint16(b1)) / 3),
		}
		palette[3] = [3]uint8{
			uint8((uint16(r0) + uint16(r1)*2) / 3),
			uint8((uint16(g0) + uint16(g1)*2) / 3),
			uint8((uint16(b0) + uint16(b1)*2) / 3),
		}
	}

	var indices uint32
	for i := 0; i < 16; i++ {
		var idx uint32
		if useAlpha && alpha[i] {
			idx = 3
		} else {
			idx = nearestIndex(colors[i], palette, useAlpha)
		}
		indices |= idx << (2 * uint(i))
	}

	var out [8]byte
	binary.LittleEndian.PutUint16(out[0:2], c0)
	binary.LittleEndian.PutUint16(out[2:4], c1)
	binary.LittleEndian.PutUint32(out[4:8], indices)
	return out
}

// nearestIndex returns the palette index closest to the pixel color.
func nearestIndex(px [4]uint8, palette [4][3]uint8, useAlpha bool) uint32 {
	n := 4
	if useAlpha {
		n = 3
	}
	best := uint32(0)
	bestDist := -1
	for i := 0; i < n; i++ {
		dr := int(px[0]) - int(palette[i][0])
		dg := int(px[1]) - int(palette[i][1])
		db := int(px[2]) - int(palette[i][2])
		dist := dr*dr + dg*dg + db*db
		if bestDist < 0 || dist < bestDist {
			bestDist = dist
			best = uint32(i)
		}
	}
	return best
}

// Encode serializes a processed texture into the TTEX container bytes.
func Encode(p *Processed) ([]byte, error) {
	if p == nil || p.Width <= 0 || p.Height <= 0 {
		return nil, ErrDimensions
	}
	var buf bytes.Buffer
	buf.WriteString(texMagic)
	buf.WriteByte(FormatVersion)
	buf.WriteByte(byte(p.Format))
	var flags byte
	if p.HasAlpha {
		flags |= texFlagAlpha
	}
	buf.WriteByte(flags)
	var hdr [12]byte
	binary.LittleEndian.PutUint32(hdr[0:4], uint32(p.Width))
	binary.LittleEndian.PutUint32(hdr[4:8], uint32(p.Height))
	binary.LittleEndian.PutUint32(hdr[8:12], uint32(len(p.Data)))
	buf.Write(hdr[:])
	buf.Write(p.Data)
	sum := sha256.Sum256(buf.Bytes())
	buf.Write(sum[:])
	return buf.Bytes(), nil
}

// DecodeContainer parses TTEX container bytes back into a Processed.
func DecodeContainer(b []byte) (*Processed, error) {
	if len(b) < 4+1+1+1+12+32 {
		return nil, ErrCorrupt
	}
	if string(b[0:4]) != texMagic {
		return nil, ErrBadMagic
	}
	if b[4] != FormatVersion {
		return nil, ErrBadVersion
	}
	format := PixelFormat(b[5])
	flags := b[6]
	width := int(binary.LittleEndian.Uint32(b[7:11]))
	height := int(binary.LittleEndian.Uint32(b[11:15]))
	dataLen := int(binary.LittleEndian.Uint32(b[15:19]))
	if width <= 0 || height <= 0 || format != PixelFormatBC1 {
		return nil, ErrCorrupt
	}
	expected := 4 + 1 + 1 + 1 + 12 + dataLen + 32
	if len(b) != expected {
		return nil, ErrCorrupt
	}
	payload := b[:len(b)-32]
	sum := sha256.Sum256(payload)
	if !bytes.Equal(sum[:], b[len(b)-32:]) {
		return nil, ErrCorrupt
	}
	data := make([]byte, dataLen)
	copy(data, b[19:19+dataLen])
	return &Processed{
		Width:    width,
		Height:   height,
		Format:   format,
		HasAlpha: flags&texFlagAlpha != 0,
		Data:     data,
	}, nil
}
