// Command tap builds deterministic texture asset packages.
//
// Usage:
//
//	tap -src <dir> -out <file.pak> [flags]
//
// Flags:
//
//	-cache dir       persistent build cache directory
//	-jobs n          processing parallelism (default GOMAXPROCS)
//	-level 1..9      compression level (default 6)
//	-lenient         skip bad assets and record diagnostics instead of failing
//	-timeout-ms n    per-asset processing timeout (0 = none)
//	-max-cache bytes  bound cache size; older entries are evicted first
package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/shikejing/texture-asset-pipeline-go/cache"
	"github.com/shikejing/texture-asset-pipeline-go/config"
	"github.com/shikejing/texture-asset-pipeline-go/pipeline"
)

func main() {
	var (
		src       = flag.String("src", "", "source asset directory")
		out       = flag.String("out", "", "output package path")
		cacheDir  = flag.String("cache", "", "build cache directory")
		jobs      = flag.Int("jobs", 0, "parallel workers (default GOMAXPROCS)")
		level     = flag.Int("level", 6, "compression level 1..9")
		lenient   = flag.Bool("lenient", false, "skip bad assets instead of failing")
		timeoutMS = flag.Int("timeout-ms", 0, "per-asset timeout in ms")
		maxCache  = flag.Int64("max-cache", 0, "max cache bytes (0 = unlimited)")
	)
	flag.Parse()
	if *src == "" || *out == "" {
		fmt.Fprintln(os.Stderr, "usage: tap -src <dir> -out <file.pak> [flags]")
		flag.PrintDefaults()
		os.Exit(2)
	}

	cfg := config.Default()
	cfg.CompressionLevel = config.CompressionLevel(*level)
	if *lenient {
		cfg.OnError = config.OnErrorLenient
	}
	opts := config.DefaultOptions(context.Background())
	opts.Jobs = *jobs
	opts.CacheDir = *cacheDir
	opts.MaxCacheBytes = *maxCache
	opts.PerAssetTimeoutMS = *timeoutMS

	var c *cache.Cache
	if *cacheDir != "" {
		var err error
		c, err = cache.Open(*cacheDir)
		if err != nil {
			fatal(err)
		}
		defer c.Close()
	}

	rep, err := pipeline.New(c).Build(context.Background(), *src, *out, cfg, opts)
	if err != nil {
		fatal(err)
	}
	for _, r := range rep.Reports {
		switch r.Status {
		case pipeline.StatusFailed:
			fmt.Printf("%-12s %s (%s)\n", r.Status, r.Path, r.Reason)
		default:
			fmt.Printf("%-12s %s\n", r.Status, r.Path)
		}
	}
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "tap:", err)
	os.Exit(1)
}
