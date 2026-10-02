package cache

import (
	"os"
	"path/filepath"
	"sort"
)

type entryStat struct {
	key  string
	size int64
	// atime/mtime are used only to break ties deterministically via nanos.
	modNs int64
}

// Evict enforces the configured maximum on-disk cache size.
// Eviction order is deterministic: least-recently modified first, then the
// key lexicographically. Overhead from the VERSION stamp is ignored.
func (c *Cache) Evict(maxBytes int64) error {
	if maxBytes <= 0 {
		return nil
	}
	dir := filepath.Join(c.dir, resultsDir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	var stats []entryStat
	var total int64
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		stats = append(stats, entryStat{
			key: e.Name(), size: info.Size(), modNs: info.ModTime().UnixNano(),
		})
		total += info.Size()
	}
	if total <= maxBytes {
		return nil
	}
	sort.Slice(stats, func(i, j int) bool {
		if stats[i].modNs != stats[j].modNs {
			return stats[i].modNs < stats[j].modNs
		}
		return stats[i].key < stats[j].key
	})
	for _, s := range stats {
		if total <= maxBytes {
			break
		}
		if err := os.Remove(filepath.Join(dir, s.key)); err != nil && !os.IsNotExist(err) {
			return err
		}
		total -= s.size
	}
	return nil
}
