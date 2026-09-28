package compress

import (
	"bytes"
	"io"
	"testing"
)

// benchPayload generates semi-compressible data (repeated text with slight variation),
// closer to a real layer.
func benchPayload(size int) []byte {
	buf := make([]byte, 0, size)
	block := []byte("oci-squash layer content line with some entropy 0123456789\n")
	for len(buf) < size {
		buf = append(buf, block...)
	}
	return buf[:size]
}

// BenchmarkBuildLayer measures the throughput of the single-pass parallel compression + dual
// hash pipeline, per algorithm.
// Run: go test -bench=BuildLayer -benchmem ./internal/compress/
func BenchmarkBuildLayer(b *testing.B) {
	const size = 32 << 20 // 32 MiB
	payload := benchPayload(size)

	for _, algo := range []Algorithm{None, Gzip, Zstd} {
		b.Run(string(algo), func(b *testing.B) {
			b.SetBytes(int64(size))
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				_, cleanup, err := BuildLayer(b.TempDir(), Options{Algorithm: algo}, func(w io.Writer) error {
					_, err := io.Copy(w, bytes.NewReader(payload))
					return err
				})
				if err != nil {
					b.Fatal(err)
				}
				cleanup()
			}
		})
	}
}
