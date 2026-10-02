package cache

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestPutGetRoundTrip(t *testing.T) {
	c, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	key := "abc123"
	data := []byte("hello world")
	if err := c.Put(key, data); err != nil {
		t.Fatal(err)
	}
	got, err := c.Get(key)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, data) {
		t.Fatalf("got %q, want %q", got, data)
	}
}

func TestMiss(t *testing.T) {
	c, _ := New(t.TempDir())
	_, err := c.Get("nonexistent")
	if err != ErrMiss {
		t.Fatalf("expected ErrMiss, got %v", err)
	}
}

func TestCorruptEntry(t *testing.T) {
	c, _ := New(t.TempDir())
	key := "k"
	if err := c.Put(key, []byte("data")); err != nil {
		t.Fatal(err)
	}
	// Corrupt the file.
	p := c.Path(key)
	b, _ := os.ReadFile(p)
	b[len(b)-1] ^= 0xFF
	_ = os.WriteFile(p, b, 0o644)

	_, err := c.Get(key)
	if err != ErrCorrupt {
		t.Fatalf("expected ErrCorrupt, got %v", err)
	}
	// Corrupt entry should be removed.
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Fatal("corrupt entry was not removed")
	}
}

func TestPartialWriteNotValid(t *testing.T) {
	c, _ := New(t.TempDir())
	key := "k"
	// Simulate a partial write: write a truncated entry directly.
	entry := encodeEntry(key, []byte("data"))
	partial := entry[:len(entry)/2]
	_ = os.WriteFile(c.Path(key), partial, 0o644)
	_, err := c.Get(key)
	if err != ErrCorrupt {
		t.Fatalf("expected ErrCorrupt for partial write, got %v", err)
	}
}

func TestNoTempFilesLeft(t *testing.T) {
	c, _ := New(t.TempDir())
	for i := 0; i < 10; i++ {
		if err := c.Put("key"+string(rune('a'+i)), []byte("data")); err != nil {
			t.Fatal(err)
		}
	}
	entries, _ := os.ReadDir(c.Dir())
	for _, e := range entries {
		if filepath.Ext(e.Name()) != ".cache" {
			t.Fatalf("unexpected non-cache file: %s", e.Name())
		}
	}
}

func TestRemove(t *testing.T) {
	c, _ := New(t.TempDir())
	c.Put("k", []byte("data"))
	if err := c.Remove("k"); err != nil {
		t.Fatal(err)
	}
	_, err := c.Get("k")
	if err != ErrMiss {
		t.Fatalf("expected ErrMiss after remove, got %v", err)
	}
}
