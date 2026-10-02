# tap — Texture Asset Pipeline

A deterministic, incremental, concurrent texture asset compression and
packaging pipeline. It turns a source directory of images and descriptors
into a single runtime-loadable resource package, maintaining a reusable
build cache so that only changed assets are reprocessed.

## Properties

- **Deterministic**: the same inputs and configuration always produce a
  byte-identical package, regardless of directory traversal order,
  concurrent completion order, or runtime randomization.
- **Incremental**: only assets whose content or processing configuration
  changed are reprocessed; everything else is reused from the cache.
  Incremental output is byte-identical to a full rebuild.
- **Content-deduplicated**: identical content is stored once. References
  between assets are rewritten to content-addressed tokens that resolve to
  the final on-disk location.
- **Concurrent with bounded resources**: a worker pool processes assets in
  parallel; in-flight memory and disk usage are capped by configurable
  semaphores. Per-asset failures and timeouts are isolated and do not
  abort the batch. Cancellation leaves no half-written state.
- **Reliable cache**: cache entries are keyed by content and processing
  configuration, checksum-verified, and atomically written. Corrupt or
  partial entries are detected and treated as misses. Configuration or
  algorithm version changes invalidate the relevant entries.
- **Fault-tolerant**: corrupt or illegal inputs produce locatable errors,
  are excluded from the manifest, and never result in a partial package.
- **Versioned**: the package and manifest carry format and codec version
  markers. Loaders reject packages that are too old or too new.

## Build

```sh
go build -o tap .
```

Requires Go 1.25 or later. No external dependencies (standard library
only).

## Usage

### Build a package

```sh
tap build -src <source-dir> -out <output.pkg> -cache <cache-dir> [options]
```

Options:

| Flag | Default | Description |
|------|---------|-------------|
| `-workers` | 4 | Number of concurrent processing workers |
| `-mem-limit` | 256 MiB | Approximate cap on in-flight decoded bytes (0 = unlimited) |
| `-disk-limit` | 512 MiB | Approximate cap on in-flight temp bytes (0 = unlimited) |
| `-timeout` | 30000 | Per-asset timeout in milliseconds (0 = none) |
| `-level` | 6 | Compression level (codec-specific effort/quality) |
| `-max-dim` | 4096 | Reject textures larger than this in either dimension (0 = unlimited) |

### Inspect a package

```sh
tap info -pkg <package.pkg>
```

### Verify a package

```sh
tap verify -pkg <package.pkg>
```

Exits 0 if the package is intact and the version is supported, non-zero
otherwise.

## Source assets

The source directory is scanned recursively. Two asset kinds are
recognized:

- **Textures** (`.png`, `.jpg`, `.jpeg`): decoded, validated, compressed
  with a deterministic BC1-style block codec, and stored as TTEX blobs.
- **Descriptors** (`.json`): parsed, references to other assets resolved
  and rewritten to content-addressed tokens, and re-serialized.

Any other file types are ignored.

### References

A descriptor may reference other assets through relative path strings in
its JSON values. For example:

```json
{
  "type": "material",
  "textures": {
    "diffuse": "textures/wood.png",
    "normal": "textures/wood_n.png"
  }
}
```

The pipeline resolves each relative path to a source asset, rewrites the
string to `@<content-hash>`, and records the mapping in the manifest's
`references` list. References are resolved relative to the descriptor's
own directory.

## Package format

A `.pkg` file is a binary container:

```
┌─────────────────────────────────────────────────────┐
│ Magic            5 bytes   "TAPKG"                  │
│ FormatVersion    uint32    package format version   │
│ MinReaderVersion uint32    oldest readable version  │
│ CodecVersion     uint32    codec/algorithm version  │
│ ManifestLen      uint64    manifest byte length     │
│ Manifest         JSON      (ManifestLen bytes)      │
│ Blob data        concatenated, sorted by hash       │
│ Footer           32 bytes  sha256 of all above      │
└─────────────────────────────────────────────────────┘
```

The footer hash covers every preceding byte, so any corruption is
detected on read.

### Manifest

The manifest is a JSON object:

```json
{
  "magic": "TAPKG",
  "format_version": 1,
  "min_reader_version": 1,
  "codec_version": 1,
  "asset_count": 3,
  "blob_count": 2,
  "assets": [
    {
      "rel_path": "textures/wood.png",
      "kind": "texture",
      "content_hash": "<sha256 of source bytes>",
      "blob_hash": "<sha256 of stored blob>",
      "blob_size": 128,
      "width": 16,
      "height": 16
    }
  ],
  "blobs": [
    { "hash": "<sha256>", "offset": 1309, "size": 128 }
  ],
  "references": [
    {
      "from_asset": "material.json",
      "field": "textures.diffuse",
      "to_hash": "<content-hash>",
      "to_path": "@<content-hash>"
    }
  ]
}
```

- `assets` is sorted by `rel_path`.
- `blobs` is sorted by `hash`; `offset` is the absolute file offset of the
  blob data.
- `references` is sorted by `(from_asset, field)`.

### Version compatibility

The package carries two version markers:

- `format_version`: the package container format version.
- `min_reader_version`: the oldest reader version that can read this
  package.

A reader accepts a package when `min_reader_version <= reader_version <=
format_version`. A package older than the reader's minimum is rejected
as `ErrTooOld`; a package newer than the reader's maximum is rejected as
`ErrTooNew`. This prevents an old reader from silently misinterpreting a
new-format package and vice versa.

## Cache semantics

The cache directory holds one file per unique cache key:

```
<cache-dir>/<key>.cache
```

Each entry is a checksum-wrapped blob:

```
┌──────────────────────────────────────────┐
│ Magic        4 bytes   "TCAC"            │
│ Version      1 byte    cache format      │
│ KeyLen       2 bytes                    │
│ Key          KeyLen bytes               │
│ DataLen      8 bytes                    │
│ Data         DataLen bytes              │
│ Checksum     32 bytes  sha256 of above  │
└──────────────────────────────────────────┘
```

### Cache keys

A texture's cache key is derived from:

- the literal `"v1"`,
- the asset kind (`"texture"`),
- the source content hash,
- the processing configuration hash.

A descriptor's cache key additionally includes a fingerprint of its
resolved references (field, path, content hash triples), because a
descriptor's rewritten output depends on the content of the assets it
references.

The processing configuration hash covers: compression level, maximum
dimension, mipmap toggle, and algorithm version. It explicitly excludes
operational fields (paths, worker count, memory/disk limits, timeouts),
so changing those does not invalidate the cache.

### Invalidation

- **Content change**: the source content hash changes, so the cache key
  changes. The old entry is orphaned; the asset is reprocessed.
- **Configuration change**: the processing configuration hash changes,
  so all affected cache keys change. All assets are reprocessed.
- **Algorithm version bump**: the `AlgorithmVersion` is part of the
  configuration hash, so bumping it invalidates every entry.

### Integrity

On read, the cache entry's checksum is verified. A mismatch (corruption
or partial write) causes the entry to be deleted and treated as a miss;
the asset is reprocessed and the result is re-cached. Cache writes are
atomic (temp file + fsync + rename), so a crash never leaves a
half-written entry that could be mistaken for valid.

## Configuration

The following fields control processing and contribute to the cache key:

| Field | Default | Description |
|-------|---------|-------------|
| `compression_level` | 6 | Codec quality/effort level |
| `max_dimension` | 4096 | Maximum texture dimension (reject larger) |
| `generate_mips` | false | Generate mipmaps (reserved) |
| `algorithm_version` | 1 | Bump to invalidate all cache entries |

The following fields are operational and do **not** contribute to the
cache key:

| Field | Default | Description |
|-------|---------|-------------|
| `workers` | 4 | Concurrent worker count |
| `memory_limit` | 256 MiB | In-flight decoded byte cap |
| `disk_limit` | 512 MiB | In-flight temp byte cap |
| `asset_timeout_ms` | 30000 | Per-asset timeout in ms |

## Determinism guarantees

The package output is a pure function of:

1. The set of source files and their contents.
2. The processing configuration.

It is **not** affected by:

- Directory traversal order (assets are sorted by relative path).
- Concurrent completion order (results are collected by index and
  sorted; blobs are sorted by hash).
- Runtime randomization (no maps are iterated for output; no timestamps
  or random values are written).
- Worker count or resource limits.

This is verified by tests that build the same source with different
worker counts and compare the resulting packages byte-for-byte, and that
compare a cold full rebuild with a warm incremental rebuild.

## Architecture

```
main.go              CLI entry point
internal/
  config/            Configuration and cache-key serialization
  asset/             Asset model and result types
  scan/              Directory scanner and content hashing
  codec/             Image decode and deterministic BC1 compression
  cache/             Checksum-verified atomic build cache
  pack/              TAPKG package and manifest reader/writer
  pipeline/          Orchestration, worker pool, reference resolution
```

The pipeline processes assets in phases:

1. **Scan**: discover and classify source files.
2. **Hash**: compute content hashes concurrently.
3. **Resolve**: for each descriptor, resolve references to content
   hashes.
4. **Process**: concurrently decode/compress (textures) or rewrite
   (descriptors), using the cache when possible.
5. **Pack**: assemble the manifest and write the package atomically.

Each asset reports a status: `hit` (reused from cache), `miss`
(reprocessed), or `error` (failed). The build fails if any asset errors,
and no package is written.
