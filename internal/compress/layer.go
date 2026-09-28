package compress

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"

	v1 "github.com/google/go-containerregistry/pkg/v1"
	v1types "github.com/google/go-containerregistry/pkg/v1/types"
)

// precomputedLayer is a v1.Layer whose compression and both hashes are computed once at
// construction and cached, with the compressed blob written to a temp file. This way, when the
// layer is handed to ggcr's mutate/layout for assembly, the library neither recompresses nor
// rescans to obtain the two hashes (see the technical design's "red line").
type precomputedLayer struct {
	diffID    v1.Hash // sha256 of the uncompressed tar
	digest    v1.Hash // sha256 of the compressed blob
	size      int64   // compressed size
	mediaType v1types.MediaType
	blobPath  string // temp file holding the compressed blob
}

var _ v1.Layer = (*precomputedLayer)(nil)

func (l *precomputedLayer) Digest() (v1.Hash, error)             { return l.digest, nil }
func (l *precomputedLayer) DiffID() (v1.Hash, error)             { return l.diffID, nil }
func (l *precomputedLayer) Size() (int64, error)                 { return l.size, nil }
func (l *precomputedLayer) MediaType() (v1types.MediaType, error) { return l.mediaType, nil }

func (l *precomputedLayer) Compressed() (io.ReadCloser, error) {
	return os.Open(l.blobPath)
}

// Uncompressed decompresses the blob on demand. The main squash flow does not use it (the
// config already has the diffID); it only satisfies the interface and some validation paths.
func (l *precomputedLayer) Uncompressed() (io.ReadCloser, error) {
	f, err := os.Open(l.blobPath)
	if err != nil {
		return nil, err
	}
	return newDecompressReadCloser(f, l.mediaType)
}

// BuildLayer compresses the uncompressed tar stream produced by produce into a v1.Layer in a
// single parallel pass:
//
//	produce ─▶ tee ─▶ sha256 ───────────────▶ diffID
//	                └▶ parallel compress ─▶ sha256 ──▶ digest
//	                                     └▶ temp blob file
//
// produce runs in its own goroutine and overlaps with compression via an io.Pipe. The returned
// layer owns the temp blob file; the caller should call cleanup when done.
func BuildLayer(tmpDir string, opts Options, produce func(w io.Writer) error) (v1.Layer, func() error, error) {
	blob, err := os.CreateTemp(tmpDir, "squash-blob-*.tar."+string(opts.Algorithm))
	if err != nil {
		return nil, nil, fmt.Errorf("create blob tmp: %w", err)
	}
	blobPath := blob.Name()
	cleanup := func() error { return os.Remove(blobPath) }

	diffHasher := sha256.New()   // uncompressed hash -> diffID
	digestHasher := sha256.New() // compressed hash -> digest

	// The blob file is also fed to digestHasher, computing the compressed hash and size in one pass.
	countingBlob := &countingWriter{w: io.MultiWriter(blob, digestHasher)}

	zw, err := opts.NewWriter(countingBlob)
	if err != nil {
		blob.Close()
		cleanup()
		return nil, nil, err
	}

	// produce writes into pw; pw's content is tee'd to diffHasher (uncompressed) then compressed.
	pr, pw := io.Pipe()
	produceErr := make(chan error, 1)
	go func() {
		// produce's output is the uncompressed tar bytes.
		err := produce(pw)
		pw.CloseWithError(err)
		produceErr <- err
	}()

	// Main goroutine: pr ─▶ tee(diffHasher) ─▶ zw(parallel compress) ─▶ blob+digestHasher
	tee := io.TeeReader(pr, diffHasher)
	if _, err := io.Copy(zw, tee); err != nil {
		zw.Close()
		blob.Close()
		cleanup()
		return nil, nil, fmt.Errorf("compress copy: %w", err)
	}
	if err := zw.Close(); err != nil { // flush the compression trailer
		blob.Close()
		cleanup()
		return nil, nil, fmt.Errorf("compress close: %w", err)
	}
	if err := <-produceErr; err != nil {
		blob.Close()
		cleanup()
		return nil, nil, fmt.Errorf("produce layer: %w", err)
	}
	if err := blob.Close(); err != nil {
		cleanup()
		return nil, nil, fmt.Errorf("close blob: %w", err)
	}

	layer := &precomputedLayer{
		diffID:    v1.Hash{Algorithm: "sha256", Hex: hex.EncodeToString(diffHasher.Sum(nil))},
		digest:    v1.Hash{Algorithm: "sha256", Hex: hex.EncodeToString(digestHasher.Sum(nil))},
		size:      countingBlob.n,
		mediaType: opts.MediaType(),
		blobPath:  blobPath,
	}
	return layer, cleanup, nil
}

type countingWriter struct {
	w io.Writer
	n int64
}

func (c *countingWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += int64(n)
	return n, err
}
