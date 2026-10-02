package manifest

import (
	"errors"
	"fmt"
)

// ErrUnsupportedVersion is returned when a manifest version cannot be read.
var ErrUnsupportedVersion = errors.New("manifest: unsupported version")

// CheckCompatibility verifies that a loader advertising readerMaxVersion
// (the newest format version it understands) can consume m. A manifest is
// readable when MinSupportedVersion <= its version <= readerMaxVersion.
// Newer builders never silently alter old pack semantics: they tag every
// pack with its version and loaders reject anything they do not know.
func CheckCompatibility(m *Manifest, readerMaxVersion uint16) error {
	if m.FormatVersion < MinSupportedVersion {
		return fmt.Errorf("%w: manifest v%d older than minimum v%d",
			ErrUnsupportedVersion, m.FormatVersion, MinSupportedVersion)
	}
	if m.FormatVersion > readerMaxVersion {
		return fmt.Errorf("%w: manifest v%d newer than reader supports (v%d)",
			ErrUnsupportedVersion, m.FormatVersion, readerMaxVersion)
	}
	return nil
}
