package config

import (
	"strings"
	"testing"
)

func TestCacheKeyExcludesOperationalFields(t *testing.T) {
	c1 := Default()
	c1.Source = "/a"
	c1.Output = "/b"
	c1.CacheDir = "/c"
	c1.Workers = 1
	c1.MemoryLimit = 100

	c2 := Default()
	c2.Source = "/x"
	c2.Output = "/y"
	c2.CacheDir = "/z"
	c2.Workers = 8
	c2.MemoryLimit = 999

	if c1.CacheKey() != c2.CacheKey() {
		t.Fatal("operational fields should not affect cache key")
	}
}

func TestCacheKeyChangesWithProcessingConfig(t *testing.T) {
	c1 := Default()
	c2 := Default()
	c2.CompressionLevel = 9
	if c1.CacheKey() == c2.CacheKey() {
		t.Fatal("compression level should affect cache key")
	}

	c3 := Default()
	c3.AlgorithmVersion = 2
	if c1.CacheKey() == c3.CacheKey() {
		t.Fatal("algorithm version should affect cache key")
	}
}

func TestLoad(t *testing.T) {
	c, err := Load(strings.NewReader(`{"workers": 8, "compression_level": 9}`))
	if err != nil {
		t.Fatal(err)
	}
	if c.Workers != 8 {
		t.Fatalf("workers = %d, want 8", c.Workers)
	}
	if c.CompressionLevel != 9 {
		t.Fatalf("level = %d, want 9", c.CompressionLevel)
	}
	if c.MaxDimension != 4096 {
		t.Fatalf("max dimension = %d, want 4096", c.MaxDimension)
	}
}
