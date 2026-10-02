package cache_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/shikejing/texture-asset-pipeline-go/cache"
	"github.com/shikejing/texture-asset-pipeline-go/config"
)

func sampleResult() *cache.Result {
	raw := []byte("hello raw texture")
	enc := []byte("FTX1encoded-bytes")
	return &cache.Result{
		Path: "a.png", Type: "png", Raw: raw, Encoded: enc,
	}
}

func TestPutGetRoundTrip(t *testing.T) {
	c, err := cache.Open(filepath.Join(t.TempDir(), "c"))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	r := sampleResult()
	key := cache.Key(cache.HashBytes(r.Raw), cache.ConfigFingerprint(config.Default()))
	if err := c.Put(ctx, key, r); err != nil {
		t.Fatal(err)
	}
	got, err := c.Get(ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	if string(got.Raw) != string(r.Raw) || string(got.Encoded) != string(r.Encoded) {
		t.Fatal("round trip mismatch")
	}
	if got.Path != "a.png" {
		t.Fatalf("path=%q", got.Path)
	}
}

func TestMiss(t *testing.T) {
	c, _ := cache.Open(filepath.Join(t.TempDir(), "c"))
	if _, err := c.Get(context.Background(), "deadbeef"); !errors.Is(err, cache.ErrMiss) {
		t.Fatalf("want ErrMiss, got %v", err)
	}
}

func corrupt(t *testing.T, path string, mutate func(b []byte) []byte) {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, mutate(b), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestCorruptEntryIsRejectedAndPurged(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "c")
	c, _ := cache.Open(dir)
	ctx := context.Background()
	r := sampleResult()
	key := cache.Key(cache.HashBytes(r.Raw), cache.ConfigFingerprint(config.Default()))
	if err := c.Put(ctx, key, r); err != nil {
		t.Fatal(err)
	}
	entry := filepath.Join(dir, "results", key)

	// Truncation.
	corrupt(t, entry, func(b []byte) []byte { return b[:len(b)/2] })
	if _, err := c.Get(ctx, key); !errors.Is(err, cache.ErrCorrupt) {
		t.Fatalf("truncated: want ErrCorrupt, got %v", err)
	}
	if _, err := os.Stat(entry); !os.IsNotExist(err) {
		t.Fatal("corrupt entry must be purged on read")
	}

	// Bit flip in payload after re-Put.
	if err := c.Put(ctx, key, r); err != nil {
		t.Fatal(err)
	}
	corrupt(t, entry, func(b []byte) []byte { b[len(b)-1] ^= 0xFF; return b })
	if _, err := c.Get(ctx, key); !errors.Is(err, cache.ErrCorrupt) {
		t.Fatalf("bitflip: want ErrCorrupt, got %v", err)
	}

	// Leftover temp files from a crash must be ignored, not read as entries.
	if err := os.WriteFile(filepath.Join(dir, "results", ".tmp-abc123"),
		[]byte("partial garbage"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Get(ctx, key); !errors.Is(err, cache.ErrMiss) {
		t.Fatalf("temp file must not satisfy a lookup, got %v", err)
	}

	// Reopening the cache sweeps crash-leftover temp files.
	if err := os.WriteFile(filepath.Join(dir, "results", ".tmp-deadbeef"),
		[]byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := cache.Open(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "results", ".tmp-deadbeef")); !os.IsNotExist(err) {
		t.Fatal("stale temp file must be swept on open")
	}
}

func TestVersionStampMismatchInvalidatesEverything(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "c")
	c, _ := cache.Open(dir)
	ctx := context.Background()
	r := sampleResult()
	key := cache.Key(cache.HashBytes(r.Raw), cache.ConfigFingerprint(config.Default()))
	if err := c.Put(ctx, key, r); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(filepath.Join(dir, "results"))
	if len(entries) != 1 {
		t.Fatalf("setup: %d entries", len(entries))
	}
	// Simulate a builder writing a newer cache format stamp.
	if err := os.WriteFile(filepath.Join(dir, "VERSION"), []byte("999\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	c2, err := cache.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c2.Get(ctx, key); !errors.Is(err, cache.ErrMiss) {
		t.Fatalf("version bump must invalidate whole cache, got %v", err)
	}
}

func TestFingerprintChangesWithConfig(t *testing.T) {
	c1 := cache.ConfigFingerprint(config.Default())
	cfg2 := config.Default()
	cfg2.CompressionLevel = 9
	c2 := cache.ConfigFingerprint(cfg2)
	cfg3 := config.Default()
	cfg3.Codec = "bogus"
	c3 := cache.ConfigFingerprint(cfg3)
	if c1 == c2 || c1 == c3 {
		t.Fatal("distinct configs must yield distinct fingerprints")
	}
	// Repeated fingerprinting is stable.
	if cache.ConfigFingerprint(config.Default()) != c1 {
		t.Fatal("fingerprint not stable")
	}
}

func TestEvictHonorsLimitDeterministically(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "c")
	c, _ := cache.Open(dir)
	ctx := context.Background()
	for i := 0; i < 5; i++ {
		r := &cache.Result{
			Path: "p", Type: "png",
			Raw:     []byte{byte(i)},
			Encoded: make([]byte, 100+i),
		}
		key := cache.Key(cache.HashBytes(r.Raw), cache.ConfigFingerprint(config.Default()))
		if err := c.Put(ctx, key, r); err != nil {
			t.Fatal(err)
		}
	}
	// Entries are written within the same instant; tie-break by key.
	if err := c.Evict(250); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(filepath.Join(dir, "results"))
	var total int64
	for _, e := range entries {
		info, _ := e.Info()
		total += info.Size()
	}
	if total > 250 {
		t.Fatalf("eviction did not enforce limit: %d bytes", total)
	}
}
