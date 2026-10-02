package pipeline

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"time"

	"github.com/shikejing/texture-asset-pipeline-go/internal/asset"
	"github.com/shikejing/texture-asset-pipeline-go/internal/cache"
	"github.com/shikejing/texture-asset-pipeline-go/internal/codec"
	"github.com/shikejing/texture-asset-pipeline-go/internal/pack"
)

// processAsset resolves a single asset, using the cache when possible.
func (p *Pipeline) processAsset(ctx context.Context, a asset.Asset, refs []refPlan, mem, disk *byteSemaphore) *processed {
	start := time.Now()
	res := &processed{Result: asset.Result{Asset: a}}

	// References are derived from the resolved ref plans, which are available
	// for every descriptor regardless of cache status. This keeps the manifest
	// consistent between hits and misses.
	res.references = buildReferences(a.RelPath, refs)

	var cacheKey string
	switch a.Kind {
	case asset.KindTexture:
		cacheKey = contentHashKey("v1", "texture", a.ContentHash, p.cfg.CacheKey())
	case asset.KindDescriptor:
		refParts := make([]string, 0, len(refs)*3)
		for _, r := range refs {
			refParts = append(refParts, r.field, r.path, r.contentHash)
		}
		sort.Strings(refParts)
		fp := contentHashKey(refParts...)
		cacheKey = contentHashKey("v1", "descriptor", a.ContentHash, p.cfg.CacheKey(), fp)
	default:
		res.Status = asset.StatusError
		res.Err = fmt.Errorf("unknown asset kind %q", a.Kind)
		res.Duration = time.Since(start)
		return res
	}

	// Try the cache.
	if cached, err := p.cache.Get(cacheKey); err == nil {
		res.Status = asset.StatusHit
		res.blobBytes = cached
		res.BlobHash = sha256Hex(cached)
		res.BlobSize = int64(len(cached))
		p.fillTextureMeta(res, cached)
		res.Duration = time.Since(start)
		return res
	} else if err != nil && err != cache.ErrMiss {
		// A non-miss cache error is non-fatal; fall through to reprocess.
	}

	// Apply per-asset timeout.
	procCtx := ctx
	var cancel context.CancelFunc
	if p.cfg.AssetTimeoutMS > 0 {
		procCtx, cancel = context.WithTimeout(ctx, time.Duration(p.cfg.AssetTimeoutMS)*time.Millisecond)
		defer cancel()
	}

	// Estimate resource usage.
	var estMem, estDisk int64
	if a.Kind == asset.KindTexture {
		estMem = a.Size * 4
		estDisk = a.Size
	} else {
		estMem = a.Size
		estDisk = a.Size
	}

	var blob []byte
	var perr error

	done := make(chan struct{})
	go func() {
		defer close(done)
		mem.acquire(estMem)
		disk.acquire(estDisk)
		defer mem.release(estMem)
		defer disk.release(estDisk)
		if a.Kind == asset.KindTexture {
			blob, perr = p.processTexture(procCtx, a)
		} else {
			blob, perr = p.processDescriptor(procCtx, a, refs)
		}
	}()

	select {
	case <-done:
	case <-procCtx.Done():
		// Timed out. Wait for the worker goroutine to finish so it releases
		// the semaphores and we do not leave a half-written state.
		<-done
		res.Status = asset.StatusError
		res.Err = fmt.Errorf("asset %s: %w", a.RelPath, procCtx.Err())
		res.Duration = time.Since(start)
		return res
	}

	if perr != nil {
		res.Status = asset.StatusError
		res.Err = perr
		res.Duration = time.Since(start)
		return res
	}

	// Store in cache. A cache write failure is non-fatal.
	_ = p.cache.Put(cacheKey, blob)

	res.Status = asset.StatusMiss
	res.blobBytes = blob
	res.BlobHash = sha256Hex(blob)
	res.BlobSize = int64(len(blob))
	p.fillTextureMeta(res, blob)
	res.Duration = time.Since(start)
	return res
}

// buildReferences converts resolved ref plans into manifest references.
func buildReferences(fromAsset string, refs []refPlan) []pack.Reference {
	out := make([]pack.Reference, 0, len(refs))
	for _, r := range refs {
		out = append(out, pack.Reference{
			FromAsset: fromAsset,
			Field:     r.field,
			ToHash:    r.contentHash,
			ToPath:    "@" + r.contentHash,
		})
	}
	return out
}

// fillTextureMeta decodes a texture blob to populate width/height/alpha.
func (p *Pipeline) fillTextureMeta(res *processed, blob []byte) {
	if res.Asset.Kind != asset.KindTexture {
		return
	}
	if proc, err := codec.DecodeContainer(blob); err == nil {
		res.width = proc.Width
		res.height = proc.Height
		res.hasAlpha = proc.HasAlpha
	}
}

// processTexture decodes, validates, compresses, and encodes a texture.
func (p *Pipeline) processTexture(ctx context.Context, a asset.Asset) ([]byte, error) {
	f, err := os.Open(a.AbsPath)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", a.RelPath, err)
	}
	defer f.Close()
	tex, err := codec.Decode(f)
	if err != nil {
		return nil, fmt.Errorf("decode %s: %w", a.RelPath, err)
	}
	if p.cfg.MaxDimension > 0 && (tex.Width > p.cfg.MaxDimension || tex.Height > p.cfg.MaxDimension) {
		return nil, fmt.Errorf("texture %s is %dx%d, exceeds max dimension %d",
			a.RelPath, tex.Width, tex.Height, p.cfg.MaxDimension)
	}
	proc, err := codec.Compress(tex, p.cfg.CompressionLevel)
	if err != nil {
		return nil, fmt.Errorf("compress %s: %w", a.RelPath, err)
	}
	blob, err := codec.Encode(proc)
	if err != nil {
		return nil, fmt.Errorf("encode %s: %w", a.RelPath, err)
	}
	return blob, nil
}

// processDescriptor parses a descriptor, rewrites references, and re-marshals.
func (p *Pipeline) processDescriptor(ctx context.Context, a asset.Asset, refs []refPlan) ([]byte, error) {
	data, err := os.ReadFile(a.AbsPath)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", a.RelPath, err)
	}
	var doc interface{}
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("parse %s: %w", a.RelPath, err)
	}
	pathToHash := make(map[string]string, len(refs))
	for _, r := range refs {
		pathToHash[r.path] = r.contentHash
	}
	var references []pack.Reference
	rewritten := rewriteTree(doc, "", pathToHash, &references, a.RelPath)
	out, err := json.Marshal(rewritten)
	if err != nil {
		return nil, fmt.Errorf("marshal %s: %w", a.RelPath, err)
	}
	return out, nil
}

// rewriteTree walks a JSON tree and replaces reference strings with
// "@<content-hash>" tokens.
func rewriteTree(node interface{}, path string, pathToHash map[string]string, refs *[]pack.Reference, fromAsset string) interface{} {
	switch v := node.(type) {
	case map[string]interface{}:
		out := make(map[string]interface{}, len(v))
		for key, val := range v {
			child := key
			if path != "" {
				child = path + "." + key
			}
			out[key] = rewriteTree(val, child, pathToHash, refs, fromAsset)
		}
		return out
	case []interface{}:
		out := make([]interface{}, len(v))
		for i, val := range v {
			child := fmt.Sprintf("%s[%d]", path, i)
			out[i] = rewriteTree(val, child, pathToHash, refs, fromAsset)
		}
		return out
	case string:
		if h, ok := pathToHash[v]; ok {
			*refs = append(*refs, pack.Reference{
				FromAsset: fromAsset,
				Field:     path,
				ToHash:    h,
				ToPath:    "@" + h,
			})
			return "@" + h
		}
		return v
	default:
		return node
	}
}

// sha256Hex returns the hex-encoded sha256 of b.
func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
