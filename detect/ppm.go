package detect

import (
	"fmt"
)

// isPPM reports whether data starts with a P6 PPM signature.
func isPPM(data []byte) bool {
	return len(data) >= 3 && data[0] == 'P' && data[1] == '6' &&
		(data[2] == ' ' || data[2] == '\t' || data[2] == '\n' || data[2] == '\r')
}

// parsePPM parses a binary P6 PPM, returning dimensions, maxval and the
// exact raster payload. It enforces that all declared pixels are present
// and that no trailing garbage follows the raster.
func parsePPM(data []byte) (width, height, maxval int, payload []byte, err error) {
	bad := func(msg string, args ...any) (int, int, int, []byte, error) {
		return 0, 0, 0, nil, fmt.Errorf("%w: ppm: %s", ErrCorruptInput, fmt.Sprintf(msg, args...))
	}
	if !isPPM(data) {
		return bad("not a P6 file")
	}
	pos := 2
	readToken := func() (string, bool) {
		for pos < len(data) {
			c := data[pos]
			if c == ' ' || c == '\t' || c == '\n' || c == '\r' {
				pos++
				continue
			}
			break
		}
		start := pos
		for pos < len(data) {
			c := data[pos]
			if c == ' ' || c == '\t' || c == '\n' || c == '\r' {
				break
			}
			// Comments may only start at whitespace; a '#' inside a token
			// is malformed.
			if c == '#' {
				return "", false
			}
			pos++
		}
		if start == pos {
			return "", false
		}
		return string(data[start:pos]), true
	}
	tokW, ok := readToken()
	if !ok {
		return bad("missing width")
	}
	tokH, ok := readToken()
	if !ok {
		return bad("missing height")
	}
	tokM, ok := readToken()
	if !ok {
		return bad("missing maxval")
	}
	if _, err := fmt.Sscanf(tokW, "%d", &width); err != nil || width <= 0 || width > maxDimension {
		return bad("illegal width %q", tokW)
	}
	if _, err := fmt.Sscanf(tokH, "%d", &height); err != nil || height <= 0 || height > maxDimension {
		return bad("illegal height %q", tokH)
	}
	if _, err := fmt.Sscanf(tokM, "%d", &maxval); err != nil || maxval <= 0 || maxval > 65535 {
		return bad("illegal maxval %q", tokM)
	}
	// Exactly one single whitespace character terminates the header.
	if pos >= len(data) {
		return bad("truncated header")
	}
	pos++
	bpp := 3
	if maxval > 255 {
		bpp = 6
	}
	need := width * height * bpp
	if len(data)-pos != need {
		return bad("raster length %d, want %d", len(data)-pos, need)
	}
	return width, height, maxval, data[pos:], nil
}
