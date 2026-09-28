// Command oci-squash merges layers of OCI/Docker images.
package main

import (
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"oci-squash/internal/compress"
	"oci-squash/internal/oci"
	"oci-squash/internal/squash"
)

func main() {
	if err := newRootCmd().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

type flags struct {
	from        string
	output      string
	format      string
	compression string
	level       int
	tag         string
	message     string
	tmpDir      string
	parallel    int
	verbose     bool
}

func newRootCmd() *cobra.Command {
	f := &flags{}
	cmd := &cobra.Command{
		Use:   "oci-squash [flags] <source>",
		Short: "Merge layers of an OCI/Docker image",
		Long: `oci-squash merges the top layers of an image into one, reducing layer count and size.

<source> supports:
  oci:PATH               OCI layout directory
  oci-archive:FILE.tar   OCI archive
  docker-archive:FILE    docker save output
  registry reference     pulled remotely (e.g. alpine:3.20)`,
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return run(args[0], f)
		},
	}

	fl := cmd.Flags()
	fl.StringVarP(&f.from, "from", "f", "", "squash the last N layers (integer); omit for all")
	fl.StringVarP(&f.output, "output", "o", "", "output path (required)")
	fl.StringVar(&f.format, "format", "oci", "output format: oci | oci-archive | docker-archive")
	fl.StringVar(&f.compression, "compression", "zstd", "compression: zstd | gzip | none")
	fl.IntVar(&f.level, "compression-level", 0, "compression level (0 = fast default)")
	fl.StringVarP(&f.tag, "tag", "t", "", "result image tag (name:tag)")
	fl.StringVarP(&f.message, "message", "m", "", "history comment for the squashed layer")
	fl.StringVar(&f.tmpDir, "tmp-dir", "", "temp directory for compression")
	fl.IntVarP(&f.parallel, "parallel", "j", 0, "compression parallelism (0 = CPU count)")
	fl.BoolVarP(&f.verbose, "verbose", "v", false, "verbose logging")
	return cmd
}

func run(source string, f *flags) error {
	if f.output == "" {
		return fmt.Errorf("--output is required")
	}
	algo, err := compress.ParseAlgorithm(f.compression)
	if err != nil {
		return err
	}
	format, err := oci.ParseFormat(f.format)
	if err != nil {
		return err
	}

	logf := func(format string, a ...any) {
		if f.verbose {
			fmt.Fprintf(os.Stderr, format+"\n", a...)
		}
	}

	start := time.Now()
	logf("loading source: %s", source)
	img, err := oci.LoadImage(source)
	if err != nil {
		return err
	}

	logf("merging layers (compression=%s level=%d parallel=%d)", algo, f.level, f.parallel)
	tSquash := time.Now()
	newImg, cleanup, err := squash.Squash(img, squash.Options{
		From:    f.from,
		Message: f.message,
		TmpDir:  f.tmpDir,
		Compress: compress.Options{
			Algorithm: algo,
			Level:     f.level,
			Parallel:  f.parallel,
		},
	})
	if err != nil {
		return err
	}
	defer cleanup()
	logf("merge+compress took: %s", time.Since(tSquash).Round(time.Millisecond))

	logf("writing %s -> %s", format, f.output)
	if err := oci.WriteImage(f.output, newImg, format, f.tag); err != nil {
		return err
	}

	fmt.Fprintf(os.Stderr, "done: %s (total %s)\n", f.output, time.Since(start).Round(time.Millisecond))
	return nil
}
