package pack_test

import "github.com/shikejing/texture-asset-pipeline-go/cache"

func hashOf(b []byte) string { return cache.HashBytes(b) }
