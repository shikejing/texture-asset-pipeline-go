package pipeline_test

import "github.com/shikejing/texture-asset-pipeline-go/pack"

func openPack(path string) (*pack.Reader, error) { return pack.Open(path) }
