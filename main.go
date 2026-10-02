// Command tap builds texture asset packages.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/shikejing/texture-asset-pipeline-go/internal/config"
	"github.com/shikejing/texture-asset-pipeline-go/internal/pack"
	"github.com/shikejing/texture-asset-pipeline-go/internal/pipeline"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	switch os.Args[1] {
	case "build":
		cmdBuild(os.Args[2:])
	case "info":
		cmdInfo(os.Args[2:])
	case "verify":
		cmdVerify(os.Args[2:])
	case "-h", "--help", "help":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n", os.Args[1])
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: tap <command> [flags]")
	fmt.Fprintln(os.Stderr, "commands: build, info, verify")
}

func cmdBuild(args []string) {
	fs := flag.NewFlagSet("build", flag.ExitOnError)
	src := fs.String("src", "", "source directory")
	out := fs.String("out", "", "output package path")
	cacheDir := fs.String("cache", ".tap-cache", "cache directory")
	workers := fs.Int("workers", 0, "worker count (0 = default)")
	memLimit := fs.Int64("mem-limit", 0, "in-flight byte cap (0 = default)")
	diskLimit := fs.Int64("disk-limit", 0, "temp byte cap (0 = default)")
	timeoutMS := fs.Int64("timeout", 0, "per-asset timeout ms (0 = default)")
	level := fs.Int("level", 0, "compression level (0 = default)")
	maxDim := fs.Int("max-dim", 0, "max texture dimension (0 = default)")
	fs.Parse(args)
	if *src == "" || *out == "" {
		fmt.Fprintln(os.Stderr, "build requires -src and -out")
		os.Exit(2)
	}
	cfg := config.Default()
	cfg.Source = *src
	cfg.Output = *out
	cfg.CacheDir = *cacheDir
	if *workers > 0 {
		cfg.Workers = *workers
	}
	if *memLimit > 0 {
		cfg.MemoryLimit = *memLimit
	}
	if *diskLimit > 0 {
		cfg.DiskLimit = *diskLimit
	}
	if *timeoutMS > 0 {
		cfg.AssetTimeoutMS = *timeoutMS
	}
	if *level > 0 {
		cfg.CompressionLevel = *level
	}
	if *maxDim > 0 {
		cfg.MaxDimension = *maxDim
	}
	p, err := pipeline.New(cfg)
	if err != nil {
		fatal(err)
	}
	report, err := p.Build(context.Background())
	if err != nil {
		fatal(err)
	}
	fmt.Printf("built %s: %d assets (%d hit, %d miss, %d error)\n",
		report.OutputPath, report.Total, report.Hits, report.Misses, report.Errors)
	if report.Errors > 0 {
		os.Exit(1)
	}
}

func cmdInfo(args []string) {
	fs := flag.NewFlagSet("info", flag.ExitOnError)
	pkg := fs.String("pkg", "", "package path")
	fs.Parse(args)
	if *pkg == "" {
		fmt.Fprintln(os.Stderr, "info requires -pkg")
		os.Exit(2)
	}
	r := pack.NewReader(*pkg)
	m, err := r.ReadManifest()
	if err != nil {
		fatal(err)
	}
	fmt.Printf("package: %s\n", *pkg)
	fmt.Printf("format_version: %d (min_reader %d)\n", m.FormatVersion, m.MinReaderVersion)
	fmt.Printf("codec_version: %d\n", m.CodecVersion)
	fmt.Printf("assets: %d, blobs: %d, references: %d\n", m.AssetCount, m.BlobCount, len(m.References))
}

func cmdVerify(args []string) {
	fs := flag.NewFlagSet("verify", flag.ExitOnError)
	pkg := fs.String("pkg", "", "package path")
	fs.Parse(args)
	if *pkg == "" {
		fmt.Fprintln(os.Stderr, "verify requires -pkg")
		os.Exit(2)
	}
	r := pack.NewReader(*pkg)
	if err := r.Verify(); err != nil {
		fatal(err)
	}
	fmt.Println("ok")
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "error:", err)
	os.Exit(1)
}
