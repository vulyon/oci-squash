package compress

import (
	"io"

	v1types "github.com/google/go-containerregistry/pkg/v1/types"
	"github.com/klauspost/compress/zstd"
	"github.com/klauspost/pgzip"
)

// newDecompressReadCloser decompresses the blob according to its media type. Closing it also
// closes the underlying file.
func newDecompressReadCloser(f io.ReadCloser, mt v1types.MediaType) (io.ReadCloser, error) {
	switch mt {
	case v1types.OCILayerZStd:
		zr, err := zstd.NewReader(f)
		if err != nil {
			f.Close()
			return nil, err
		}
		return &zstdReadCloser{zr: zr, under: f}, nil
	case v1types.OCIUncompressedLayer, v1types.DockerUncompressedLayer:
		return f, nil
	default: // gzip family
		zr, err := pgzip.NewReader(f)
		if err != nil {
			f.Close()
			return nil, err
		}
		return &gzipReadCloser{zr: zr, under: f}, nil
	}
}

type gzipReadCloser struct {
	zr    *pgzip.Reader
	under io.Closer
}

func (g *gzipReadCloser) Read(p []byte) (int, error) { return g.zr.Read(p) }
func (g *gzipReadCloser) Close() error {
	err := g.zr.Close()
	if cerr := g.under.Close(); err == nil {
		err = cerr
	}
	return err
}

type zstdReadCloser struct {
	zr    *zstd.Decoder
	under io.Closer
}

func (z *zstdReadCloser) Read(p []byte) (int, error) { return z.zr.Read(p) }
func (z *zstdReadCloser) Close() error {
	z.zr.Close()
	return z.under.Close()
}
