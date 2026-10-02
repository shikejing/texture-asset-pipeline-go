// Package pipeline orchestrates scanning, processing, caching, and packing.
package pipeline

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/shikejing/texture-asset-pipeline-go/internal/asset"
	"github.com/shikejing/texture-asset-pipeline-go/internal/cache"
	"github.com/shikejing/texture-asset-pipeline-go/internal/config"
	"github.com/shikejing/texture-asset-pipeline-go/internal/pack"
	"github.com/shikejing/texture-asset-pipeline-go/internal/scan"
)

// Pipeline runs the build.
type Pipeline struct {
	cfg   config.Config
	cache *cache.Cache
}

// New returns a Pipeline configured by cfg.
func New(cfg config.Config) (*Pipeline, error) {
	c, err := cache.New(cfg.CacheDir)
	if err != nil {
		return nil, err
	}
	return &Pipeline{cfg: cfg, cache: c}, nil
}

// Report summarizes a build.
type Report struct {
	Total      int
	Hits       int
	Misses     int
	Errors     int
	Assets     []asset.Result
	Manifest   *pack.Manifest
	OutputPath string
}

// refPlan records a resolved reference for a descriptor.
type refPlan struct {
	field       string
	path        string
	contentHash string
}

// processed is the internal result of processing one asset.
type processed struct {
	asset.Result
	blobBytes  []byte
	hasAlpha   bool
	width      int
	height     int
	references []pack.Reference
}

// Build scans, processes, and packs the source tree.
func (p *Pipeline) Build(ctx context.Context) (*Report, error) {
	// 1. Scan.
	assets, err := scan.New(p.cfg.Source).Scan()
	if err != nil {
		return nil, fmt.Errorf("scan: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	// 2. Hash all files concurrently.
	if err := p.hashAssets(ctx, assets); err != nil {
		return nil, err
	}

	// 3. Build path → content hash map.
	pathHash := make(map[string]string, len(assets))
	for _, a := range assets {
		pathHash[a.RelPath] = a.ContentHash
	}

	// 4. Resolve descriptor references.
	plans := make(map[string][]refPlan, len(assets))
	for _, a := range assets {
		if a.Kind == asset.KindDescriptor {
			refs, rerr := p.resolveDescriptorRefs(a, pathHash)
			if rerr != nil {
				return nil, rerr
			}
			plans[a.RelPath] = refs
		}
	}

	// 5. Process assets concurrently with caching.
	results := make([]*processed, len(assets))
	blobs := make(map[string][]byte)
	var blobsMu sync.Mutex
	sem := newSemaphore(p.cfg.Workers)
	mem := newByteSemaphore(p.cfg.MemoryLimit)
	disk := newByteSemaphore(p.cfg.DiskLimit)
	var wg sync.WaitGroup

	for i := range assets {
		if err := ctx.Err(); err != nil {
			break
		}
		a := assets[i]
		wg.Add(1)
		sem.acquire()
		go func(idx int, a asset.Asset) {
			defer wg.Done()
			defer sem.release()
			res := p.processAsset(ctx, a, plans[a.RelPath], mem, disk)
			results[idx] = res
			if res.Status == asset.StatusHit || res.Status == asset.StatusMiss {
				blobsMu.Lock()
				if _, ok := blobs[res.BlobHash]; !ok {
					blobs[res.BlobHash] = res.blobBytes
				}
				blobsMu.Unlock()
			}
		}(i, a)
	}
	wg.Wait()

	// 6. Assemble report and manifest.
	report := &Report{OutputPath: p.cfg.Output}
	var manifestAssets []pack.AssetEntry
	var references []pack.Reference
	for _, r := range results {
		if r == nil {
			continue
		}
		report.Assets = append(report.Assets, r.Result)
		report.Total++
		switch r.Status {
		case asset.StatusHit:
			report.Hits++
		case asset.StatusMiss:
			report.Misses++
		case asset.StatusError:
			report.Errors++
		}
		if r.Status != asset.StatusError {
			manifestAssets = append(manifestAssets, pack.AssetEntry{
				RelPath:     r.Asset.RelPath,
				Kind:        string(r.Asset.Kind),
				ContentHash: r.Asset.ContentHash,
				BlobHash:    r.BlobHash,
				BlobSize:    r.BlobSize,
				HasAlpha:    r.hasAlpha,
				Width:       r.width,
				Height:      r.height,
			})
			references = append(references, r.references...)
		}
	}
	sort.Slice(report.Assets, func(i, j int) bool { return report.Assets[i].Asset.RelPath < report.Assets[j].Asset.RelPath })
	sort.Slice(manifestAssets, func(i, j int) bool { return manifestAssets[i].RelPath < manifestAssets[j].RelPath })
	sort.Slice(references, func(i, j int) bool {
		if references[i].FromAsset != references[j].FromAsset {
			return references[i].FromAsset < references[j].FromAsset
		}
		return references[i].Field < references[j].Field
	})

	if report.Errors > 0 {
		return report, fmt.Errorf("%d asset(s) failed", report.Errors)
	}
	if err := ctx.Err(); err != nil {
		return report, err
	}

	// 7. Build manifest and write package.
	manifest := &pack.Manifest{
		Magic:            string(pack.Magic[:]),
		FormatVersion:    pack.FormatVersion,
		MinReaderVersion: pack.MinReaderVersion,
		CodecVersion:     p.cfg.AlgorithmVersion,
		Assets:           manifestAssets,
		References:       references,
	}
	w := pack.NewWriter(p.cfg.Output)
	if err := w.Build(manifest, blobs); err != nil {
		return report, fmt.Errorf("pack: %w", err)
	}
	report.Manifest = manifest
	return report, nil
}

// hashAssets computes the content hash of each asset concurrently.
func (p *Pipeline) hashAssets(ctx context.Context, assets []asset.Asset) error {
	sem := newSemaphore(p.cfg.Workers)
	var wg sync.WaitGroup
	var mu sync.Mutex
	var firstErr error
	for i := range assets {
		if err := ctx.Err(); err != nil {
			break
		}
		a := assets[i]
		wg.Add(1)
		sem.acquire()
		go func(idx int, a asset.Asset) {
			defer wg.Done()
			defer sem.release()
			h, err := scan.HashFile(a.AbsPath)
			if err != nil {
				mu.Lock()
				if firstErr == nil {
					firstErr = fmt.Errorf("hash %s: %w", a.RelPath, err)
				}
				mu.Unlock()
				return
			}
			mu.Lock()
			assets[idx].ContentHash = h
			mu.Unlock()
		}(i, a)
	}
	wg.Wait()
	if firstErr != nil {
		return firstErr
	}
	return ctx.Err()
}

// resolveDescriptorRefs extracts and resolves references from a descriptor.
func (p *Pipeline) resolveDescriptorRefs(a asset.Asset, pathHash map[string]string) ([]refPlan, error) {
	data, err := os.ReadFile(a.AbsPath)
	if err != nil {
		return nil, err
	}
	var doc interface{}
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("parse descriptor %s: %w", a.RelPath, err)
	}
	var refs []refPlan
	walkStrings(doc, "", func(path, val string) {
		if !isReferencePath(val) {
			return
		}
		target := val
		if !filepath.IsAbs(val) {
			descDir := filepath.Dir(a.RelPath)
			target = filepath.ToSlash(filepath.Join(descDir, val))
		}
		target = filepath.ToSlash(filepath.Clean(target))
		if h, ok := pathHash[target]; ok {
			refs = append(refs, refPlan{field: path, path: val, contentHash: h})
		}
	})
	return refs, nil
}

// isReferencePath reports whether s looks like a relative asset path.
func isReferencePath(s string) bool {
	if s == "" || strings.HasPrefix(s, "/") || strings.HasPrefix(s, "@") {
		return false
	}
	if strings.Contains(s, "://") {
		return false
	}
	return true
}

// walkStrings visits every string leaf in a JSON tree.
func walkStrings(node interface{}, path string, fn func(path, val string)) {
	switch v := node.(type) {
	case map[string]interface{}:
		keys := make([]string, 0, len(v))
		for k := range v {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			child := k
			if path != "" {
				child = path + "." + k
			}
			walkStrings(v[k], child, fn)
		}
	case []interface{}:
		for i, val := range v {
			child := fmt.Sprintf("%s[%d]", path, i)
			walkStrings(val, child, fn)
		}
	case string:
		fn(path, v)
	}
}

// contentHashKey builds a cache key from the given parts.
func contentHashKey(parts ...string) string {
	h := sha256.New()
	for _, part := range parts {
		h.Write([]byte(part))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

