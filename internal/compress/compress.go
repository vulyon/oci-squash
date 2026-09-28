// Package compress provides parallel compressors, the performance core of this tool.
//
// Key constraint (see the technical design): the merged layer must be compressed here with
// the parallel implementations (pgzip / klauspost zstd) and must never fall back to ggcr's
// single-threaded compress/gzip.
package compress

import (
	"fmt"
	"io"
	"runtime"

	v1types "github.com/google/go-containerregistry/pkg/v1/types"
	"github.com/klauspost/compress/zstd"
	"github.com/klauspost/pgzip"
)

// Algorithm is a supported compression algorithm.
type Algorithm string

const (
	Gzip Algorithm = "gzip"
	Zstd Algorithm = "zstd"
	None Algorithm = "none"
)

// ParseAlgorithm parses the --compression value.
func ParseAlgorithm(s string) (Algorithm, error) {
	switch Algorithm(s) {
	case Gzip, Zstd, None:
		return Algorithm(s), nil
	default:
		return "", fmt.Errorf("unknown compression %q (want gzip|zstd|none)", s)
	}
}

// Options controls compression. When Parallel<=0 the CPU count is used.
type Options struct {
	Algorithm Algorithm
	Level     int // 0 selects each algorithm's "fast" default
	Parallel  int
}

func (o Options) parallel() int {
	if o.Parallel > 0 {
		return o.Parallel
	}
	return runtime.NumCPU()
}

// MediaType returns the OCI layer media type for the algorithm.
func (o Options) MediaType() v1types.MediaType {
	switch o.Algorithm {
	case Zstd:
		return v1types.OCILayerZStd
	case None:
		return v1types.OCIUncompressedLayer
	default:
		return v1types.OCILayer
	}
}

// writeCloser combines the underlying writer with its Close.
type writeCloser interface {
	io.WriteCloser
}

// NewWriter returns a parallel compressing writer over w. The caller must Close it to flush
// the trailer. For None it returns a pass-through writer.
func (o Options) NewWriter(w io.Writer) (writeCloser, error) {
	switch o.Algorithm {
	case None:
		return nopWriteCloser{w}, nil
	case Gzip:
		level := o.Level
		if level == 0 {
			level = pgzip.DefaultCompression
		}
		zw, err := pgzip.NewWriterLevel(w, level)
		if err != nil {
			return nil, fmt.Errorf("pgzip: %w", err)
		}
		// Block-level parallelism: 1 MiB blocks, concurrency = parallel.
		if err := zw.SetConcurrency(1<<20, o.parallel()); err != nil {
			return nil, fmt.Errorf("pgzip concurrency: %w", err)
		}
		return zw, nil
	case Zstd:
		level := zstd.SpeedDefault
		if o.Level != 0 {
			level = zstd.EncoderLevelFromZstd(o.Level)
		}
		zw, err := zstd.NewWriter(w,
			zstd.WithEncoderLevel(level),
			zstd.WithEncoderConcurrency(o.parallel()),
		)
		if err != nil {
			return nil, fmt.Errorf("zstd: %w", err)
		}
		return zw, nil
	default:
		return nil, fmt.Errorf("unhandled algorithm %q", o.Algorithm)
	}
}

type nopWriteCloser struct{ io.Writer }

func (nopWriteCloser) Close() error { return nil }
