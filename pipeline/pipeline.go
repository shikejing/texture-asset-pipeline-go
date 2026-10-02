// Package pipeline orchestrates scanning, processing, dedup and packing.
//
// Build determinism is guaranteed because:
//
//   - directory traversal is name-sorted (see scan.go);
//   - workers return results keyed by logical path, and all assembly sorts
//     by path or blob id before serialization, so worker completion order
//     can never influence output;
//   - cache keys and blob ids derive solely from content and configuration;
//   - the pack writer emits fixed-layout, timestamp-free bytes.
//
// Cancellation: on context cancellation the build returns without replacing
// any existing pack; packs are assembled in memory and published atomically.
package pipeline

import (
	"context"
	"errors"
	"fmt"
	"os"
	"runtime"
	"sort"
	"sync"
	"time"

	"github.com/shikejing/texture-asset-pipeline-go/cache"
	"github.com/shikejing/texture-asset-pipeline-go/codec"
	"github.com/shikejing/texture-asset-pipeline-go/config"
	"github.com/shikejing/texture-asset-pipeline-go/detect"
	"github.com/shikejing/texture-asset-pipeline-go/manifest"
	"github.com/shikejing/texture-asset-pipeline-go/pack"
)

// Status reports the build disposition of one asset.
type Status string

const (
	// StatusCached means the asset was served from cache.
	StatusCached Status = "cached"
	// StatusProcessed means the asset was re-encoded from source.
	StatusProcessed Status = "processed"
	// StatusLink means the asset is an alias for another asset's payload.
	StatusLink Status = "link"
	// StatusFailed means the asset could not be processed (lenient mode).
	StatusFailed Status = "failed"
)

// AssetReport explains what happened to one asset.
type AssetReport struct {
	Path   string
	Status Status
	Reason string
}

// Report is the full outcome of a build.
type Report struct {
	// Reports covers every logical asset in stable path order.
	Reports []AssetReport
	// Diagnostics holds lenient-mode failures, never embedded in packs.
	Diagnostics []manifest.Diagnostic
}

// Cached returns paths served from cache.
func (r *Report) Cached() []string { return r.selectStatus(StatusCached) }

// Processed returns paths rebuilt from source.
func (r *Report) Processed() []string { return r.selectStatus(StatusProcessed) }

// Failed returns paths that failed (lenient builds only).
func (r *Report) Failed() []string { return r.selectStatus(StatusFailed) }

func (r *Report) selectStatus(s Status) []string {
	var out []string
	for _, x := range r.Reports {
		if x.Status == s {
			out = append(out, x.Path)
		}
	}
	return out
}

// Pipeline runs builds with a reusable cache.
type Pipeline struct {
	cache *cache.Cache
}

// New creates a pipeline backed by the given cache. A nil cache disables
// persistent reuse; every build then processes every asset.
func New(c *cache.Cache) *Pipeline { return &Pipeline{cache: c} }

// loadedAsset is one source file after the deterministic read phase.
type loadedAsset struct {
	logical string
	data    []byte
}

// processedAsset is the outcome for one non-link asset.
type processedAsset struct {
	logical     string
	typeName    string
	raw         []byte
	encoded     []byte
	rawHash     string
	encodedHash string
	width       int
	height      int
}

// Build scans srcDir, processes assets and atomically writes packPath.
// Output bytes depend only on input content and cfg. On failure (including
// cancellation) no pack is created or replaced.
func (p *Pipeline) Build(ctx context.Context, srcDir, packPath string, cfg config.Config, opts config.Options) (*Report, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	jobs := opts.Jobs
	if jobs <= 0 {
		jobs = runtime.GOMAXPROCS(0)
	}
	if jobs < 1 {
		jobs = 1
	}

	fingerprint := cache.ConfigFingerprint(cfg)
	enc, err := codec.Get(cfg.Codec)
	if err != nil {
		return nil, err
	}

	// Phase 1: deterministic scan and read.
	files, err := scanDir(srcDir)
	if err != nil {
		return nil, fmt.Errorf("scan: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	assets := make(map[string]*loadedAsset)
	sidecars := make(map[string][]byte)
	for _, f := range files {
		data, err := os.ReadFile(f.abs)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", f.logical, err)
		}
		if isSidecar(f.logical) {
			sidecars[f.logical] = data
		} else {
			assets[f.logical] = &loadedAsset{logical: f.logical, data: data}
		}
	}

	// Phase 2: parse and structurally validate sidecars and links.
	// A "link" sidecar declares a standalone virtual asset (no companion
	// file needed: its entire content is the alias). A "refs" sidecar must
	// sit next to a real asset it annotates.
	type linkInfo struct {
		target string
	}
	type refInfo struct {
		refs []string
	}
	links := make(map[string]linkInfo)
	refs := make(map[string]refInfo)
	var skipped []manifest.Diagnostic
	addFailure := func(logical, stage, reason string) {
		skipped = append(skipped, manifest.Diagnostic{Path: logical, Stage: stage, Reason: reason})
	}
	sidecarNames := make([]string, 0, len(sidecars))
	for k := range sidecars {
		sidecarNames = append(sidecarNames, k)
	}
	sort.Strings(sidecarNames)
	for _, sc := range sidecarNames {
		owner := ownerPath(sc)
		_, ownerExists := assets[owner]
		parsed, perr := parseSidecar(owner, sidecars[sc])
		if perr != nil {
			if cfg.OnError == config.OnErrorLenient {
				addFailure(owner, "sidecar", perr.Error())
				delete(assets, owner)
				continue
			}
			return nil, detect.NewStageError(owner, "sidecar", perr)
		}
		if parsed.kind == "link" {
			if ownerExists {
				err := fmt.Errorf("link sidecar %q conflicts with a real asset of the same name", sc)
				if cfg.OnError == config.OnErrorLenient {
					addFailure(owner, "link", err.Error())
					continue
				}
				return nil, detect.NewStageError(owner, "link", err)
			}
			links[owner] = linkInfo{target: parsed.target}
			continue
		}
		if !ownerExists {
			err := fmt.Errorf("reference sidecar %q has no companion asset %s", sc, owner)
			if cfg.OnError == config.OnErrorLenient {
				addFailure(owner, "sidecar", err.Error())
				continue
			}
			return nil, detect.NewStageError(owner, "sidecar", err)
		}
		refs[owner] = refInfo{refs: parsed.refs}
	}

	// Cross-validate references now that every sidecar is parsed: targets may
	// be real assets or link virtual assets.
	exists := func(p string) bool {
		_, a := assets[p]
		_, l := links[p]
		return a || l
	}
	linkNames0 := make([]string, 0, len(links))
	for n := range links {
		linkNames0 = append(linkNames0, n)
	}
	sort.Strings(linkNames0)
	dropLink := func(n string, stage, reason string) error {
		if cfg.OnError == config.OnErrorLenient {
			addFailure(n, stage, reason)
			delete(links, n)
			return nil
		}
		return detect.NewStageError(n, stage, errors.New(reason))
	}
	for _, n := range linkNames0 {
		if !exists(links[n].target) {
			if err := dropLink(n, "link", fmt.Sprintf("link target %q not found in source tree", links[n].target)); err != nil {
				return nil, err
			}
		}
	}
	refOwners := make([]string, 0, len(refs))
	for n := range refs {
		refOwners = append(refOwners, n)
	}
	sort.Strings(refOwners)
	for _, n := range refOwners {
		// Owner may have been removed earlier by an invalid sidecar.
		if _, ok := assets[n]; !ok {
			delete(refs, n)
			continue
		}
		for _, t := range refs[n].refs {
			if !exists(t) {
				err := fmt.Errorf("reference target %q not found in source tree", t)
				if cfg.OnError == config.OnErrorLenient {
					addFailure(n, "refs", err.Error())
					delete(assets, n)
					delete(refs, n)
					break
				}
				return nil, detect.NewStageError(n, "refs", err)
			}
		}
	}
	// Remove links that point at assets already dropped in lenient mode,
	// transitively so link-to-link chains whose root failed are dropped too.
	for changed := true; changed; {
		changed = false
		for _, n := range linkNames0 {
			li, ok := links[n]
			if !ok {
				continue
			}
			if !exists(li.target) {
				if err := dropLink(n, "link", fmt.Sprintf("link target %q unavailable", li.target)); err != nil {
					return nil, err
				}
				changed = true
			}
		}
	}
	// Cycle detection over links (deterministic traversal).
	for _, n := range linkNames0 {
		if _, ok := links[n]; !ok {
			continue
		}
		seen := map[string]int{}
		cur := n
		for {
			if _, ok := links[cur]; !ok {
				break // reached a real asset
			}
			seen[cur]++
			if seen[cur] > 1 {
				if err := dropLink(n, "link", "link cycle detected at "+cur); err != nil {
					return nil, err
				}
				break
			}
			cur = links[cur].target
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	// Phase 3: concurrent processing with per-asset timeout and cache.
	names := make([]string, 0, len(assets))
	for n := range assets {
		names = append(names, n)
	}
	sort.Strings(names)

	type jobResult struct {
		name      string
		res       *processedAsset
		fromCache bool
		err       error
	}
	results := make([]jobResult, len(names))
	sem := make(chan struct{}, jobs)
	var wg sync.WaitGroup
	timeout := time.Duration(opts.PerAssetTimeoutMS) * time.Millisecond

	for i, name := range names {
		wg.Add(1)
		go func(i int, name string) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				results[i] = jobResult{name: name, err: ctx.Err()}
				return
			}
			defer func() { <-sem }()

			jobCtx := ctx
			var cancel context.CancelFunc
			if timeout > 0 {
				jobCtx, cancel = context.WithTimeout(ctx, timeout)
				defer cancel()
			}
			res, fromCache, err := p.processOne(jobCtx, enc, cfg, fingerprint, assets[name])
			results[i] = jobResult{name: name, res: res, fromCache: fromCache, err: err}
		}(i, name)
	}
	wg.Wait()

	if err := ctx.Err(); err != nil {
		return nil, err
	}

	// Phase 4: collect, honoring strict/lenient semantics. Results are
	// already in sorted name order because jobs were indexed by sorted names.
	processed := make(map[string]*processedAsset)
	var report Report
	report.Diagnostics = skipped

	// Assets dropped during sidecar/link/reference validation in lenient mode
	// only produced diagnostics; make sure each of them has a failed report.
	if cfg.OnError == config.OnErrorLenient {
		reported := make(map[string]bool)
		for _, d := range report.Diagnostics {
			if !reported[d.Path] {
				report.Reports = append(report.Reports, AssetReport{
					Path: d.Path, Status: StatusFailed, Reason: d.Reason,
				})
				reported[d.Path] = true
			}
		}
	}
	for _, jr := range results {
		if jr.err != nil {
			if errors.Is(jr.err, context.DeadlineExceeded) {
				jr.err = fmt.Errorf("%w: processing exceeded timeout", jr.err)
			}
			if cfg.OnError == config.OnErrorLenient {
				stage, reason := classifyError(jr.err)
				report.Reports = append(report.Reports, AssetReport{Path: jr.name, Status: StatusFailed, Reason: reason})
				report.Diagnostics = append(report.Diagnostics, manifest.Diagnostic{
					Path: jr.name, Stage: stage, Reason: reason,
				})
				continue
			}
			return nil, fmt.Errorf("asset %q: %w", jr.name, jr.err)
		}
		processed[jr.name] = jr.res
		if jr.fromCache {
			report.Reports = append(report.Reports, AssetReport{Path: jr.name, Status: StatusCached})
		} else {
			report.Reports = append(report.Reports, AssetReport{Path: jr.name, Status: StatusProcessed})
		}
	}
	// resolveTarget maps any logical name (real asset or link alias) to the
	// real processed asset whose payload it ultimately uses.
	resolveTarget := func(name string) (*processedAsset, bool) {
		seen := map[string]bool{}
		cur := name
		for {
			if pr, ok := processed[cur]; ok {
				return pr, true
			}
			li, ok := links[cur]
			if !ok || seen[cur] {
				return nil, false
			}
			seen[cur] = true
			cur = li.target
		}
	}

	// Report link dispositions in deterministic order.
	linkNames := make([]string, 0, len(links))
	for n := range links {
		linkNames = append(linkNames, n)
	}
	sort.Strings(linkNames)
	linked := make(map[string]bool)
	for _, n := range linkNames {
		if _, ok := resolveTarget(n); !ok {
			reason := fmt.Sprintf("link target %q unavailable", links[n].target)
			report.Reports = append(report.Reports, AssetReport{Path: n, Status: StatusFailed, Reason: reason})
			report.Diagnostics = append(report.Diagnostics, manifest.Diagnostic{Path: n, Stage: "link", Reason: reason})
			linked[n] = false
			continue
		}
		report.Reports = append(report.Reports, AssetReport{Path: n, Status: StatusLink})
		linked[n] = true
	}
	// One report per logical asset (validation and processing failures can
	// both refer to the same path).
	dedupedReports := report.Reports[:0]
	seenReport := make(map[string]bool, len(report.Reports))
	for _, rr := range report.Reports {
		if seenReport[rr.Path] {
			continue
		}
		seenReport[rr.Path] = true
		dedupedReports = append(dedupedReports, rr)
	}
	report.Reports = dedupedReports
	sort.Slice(report.Reports, func(i, j int) bool { return report.Reports[i].Path < report.Reports[j].Path })
	sort.Slice(report.Diagnostics, func(i, j int) bool {
		if report.Diagnostics[i].Path != report.Diagnostics[j].Path {
			return report.Diagnostics[i].Path < report.Diagnostics[j].Path
		}
		return report.Diagnostics[i].Stage < report.Diagnostics[j].Stage
	})

	// Phase 5: content-addressed dedup of encoded payloads.
	blobByHash := make(map[string]*processedAsset)
	for _, pr := range processed {
		blobByHash[pr.encodedHash] = pr
	}
	blobHashes := make([]string, 0, len(blobByHash))
	for h := range blobByHash {
		blobHashes = append(blobHashes, h)
	}
	sort.Strings(blobHashes)

	m := &manifest.Manifest{
		FormatVersion:    manifest.FormatVersion,
		AlgorithmVersion: config.AlgorithmVersion,
		Codec:            enc.Name(),
	}
	for _, h := range blobHashes {
		pr := blobByHash[h]
		m.Blobs = append(m.Blobs, manifest.Blob{
			ID:      h,
			Codec:   enc.Name(),
			Size:    int64(len(pr.encoded)),
			RawSize: pr.int64RawSize(),
			Hash:    cache.HashBytes(pr.encoded),
		})
	}

	// Assets in stable path order; links alias their target's blob.
	allNames := make([]string, 0, len(processed))
	for n := range processed {
		allNames = append(allNames, n)
	}
	for n := range links {
		if linked[n] {
			allNames = append(allNames, n)
		}
	}
	sort.Strings(allNames)
	for _, n := range allNames {
		pr := processed[n] // nil for link-only virtual assets
		isLink := false
		var li linkInfo
		if x, ok := links[n]; ok && linked[n] {
			isLink = true
			li = x
		}
		var assetType manifest.AssetType
		var blobID, rawHash string
		var size int64
		switch {
		case isLink:
			assetType = manifest.TypeLink
			tpr, ok := resolveTarget(n)
			if !ok {
				return nil, detect.NewStageError(n, "link", errors.New("link target unavailable"))
			}
			blobID = tpr.encodedHash
			rawHash = "" // link-only assets have no raw content of their own
			size = 0
		default:
			assetType = manifest.AssetType(pr.typeName)
			blobID = pr.encodedHash
			rawHash = pr.rawHash
			size = int64(len(pr.encoded))
		}
		var manifestRefs []manifest.Ref
		if ri, ok := refs[n]; ok {
			for _, t := range ri.refs {
				var targetBlob string
				if tpr, ok := resolveTarget(t); ok {
					targetBlob = tpr.encodedHash
				}
				manifestRefs = dedupAppendRef(manifestRefs, manifest.Ref{
					Kind:       manifestRefKindDependency,
					Path:       t,
					TargetBlob: targetBlob,
				})
			}
		}
		// A link records its direct target as a resolved reference.
		if isLink {
			tpr, ok := resolveTarget(li.target)
			if !ok {
				return nil, detect.NewStageError(n, "link", errors.New("link target unavailable"))
			}
			manifestRefs = dedupAppendRef(manifestRefs, manifest.Ref{
				Kind: manifest.RefLink, Path: li.target, TargetBlob: tpr.encodedHash,
			})
		}
		m.Assets = append(m.Assets, manifest.Asset{
			Path: n, Type: assetType,
			Blob: blobID, Size: size,
			RawHash: rawHash, Refs: manifestRefs,
		})
	}

	// Phase 6: assemble payloads in blob order and publish atomically.
	entries := make([]pack.Entry, 0, len(blobHashes))
	for _, h := range blobHashes {
		entries = append(entries, pack.Entry{ID: h, Data: blobByHash[h].encoded})
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := pack.Write(packPath, m, entries); err != nil {
		return nil, err
	}

	// Opportunistic, best-effort cache size enforcement.
	if p.cache != nil && opts.MaxCacheBytes > 0 {
		_ = p.cache.Evict(opts.MaxCacheBytes)
	}
	return &report, nil
}

// processOne identifies, validates and encodes one asset, consulting the
// cache. It returns the result and whether it came from cache.
func (p *Pipeline) processOne(ctx context.Context, enc codec.Codec, cfg config.Config, fingerprint string, a *loadedAsset) (*processedAsset, bool, error) {
	rawHash := cache.HashBytes(a.data)
	key := cache.Key(rawHash, fingerprint)
	if p.cache != nil {
		if cached, err := p.cache.Get(ctx, key); err == nil {
			return &processedAsset{
				logical: a.logical, typeName: cached.Type,
				raw: cached.Raw, encoded: cached.Encoded,
				rawHash: cached.RawHash, encodedHash: cached.EncodedHash,
			}, true, nil
		} else if !errors.Is(err, cache.ErrMiss) && !errors.Is(err, cache.ErrCorrupt) {
			return nil, false, err
		}
	}
	info, err := detect.Probe(a.logical, a.data)
	if err != nil {
		return nil, false, err
	}
	encoded, err := enc.Encode(ctx, a.data, cfg.CompressionLevel)
	if err != nil {
		return nil, false, err
	}
	encodedHash := cache.HashBytes(encoded)
	res := &processedAsset{
		logical: a.logical, typeName: string(info.Format),
		raw: a.data, encoded: encoded,
		rawHash: rawHash, encodedHash: encodedHash,
		width: info.Width, height: info.Height,
	}
	if p.cache != nil {
		// Cancellation after encoding still means we skip the write.
		if err := ctx.Err(); err != nil {
			return nil, false, err
		}
		cr := &cache.Result{
			Path: a.logical, Type: string(info.Format),
			Raw: a.data, Encoded: encoded,
			RawHash: rawHash, EncodedHash: encodedHash,
			RawSize: int64(len(a.data)), EncodedSize: int64(len(encoded)),
		}
		if err := p.cache.Put(ctx, key, cr); err != nil {
			return nil, false, err
		}
	}
	return res, false, nil
}

func dedupAppendRef(refs []manifest.Ref, r manifest.Ref) []manifest.Ref {
	for _, x := range refs {
		if x.Kind == r.Kind && x.Path == r.Path {
			return refs
		}
	}
	return append(refs, r)
}

// classifyError extracts a stable stage name and reason for diagnostics.
func classifyError(err error) (string, string) {
	var se *detect.StageError
	if errors.As(err, &se) {
		return se.Stage, se.Error()
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "timeout", err.Error()
	}
	if errors.Is(err, context.Canceled) {
		return "cancel", err.Error()
	}
	return "process", err.Error()
}

func (pr *processedAsset) int64RawSize() int64 { return int64(len(pr.raw)) }
