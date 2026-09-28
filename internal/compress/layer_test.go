package compress

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"testing"
)

// TestBuildLayer_RoundTrip verifies that the compressed layer's DiffID equals the sha256 of
// the uncompressed content, and that Uncompressed() restores the original bytes. Covers the
// gzip / zstd / none tiers.
func TestBuildLayer_RoundTrip(t *testing.T) {
	payload := bytes.Repeat([]byte("hello oci-squash compression pipeline\n"), 4096)
	want := sha256.Sum256(payload)
	wantHex := hex.EncodeToString(want[:])

	for _, algo := range []Algorithm{Gzip, Zstd, None} {
		t.Run(string(algo), func(t *testing.T) {
			opts := Options{Algorithm: algo}
			layer, cleanup, err := BuildLayer(t.TempDir(), opts, func(w io.Writer) error {
				_, err := w.Write(payload)
				return err
			})
			if err != nil {
				t.Fatalf("BuildLayer: %v", err)
			}
			defer cleanup()

			diffID, err := layer.DiffID()
			if err != nil {
				t.Fatal(err)
			}
			if diffID.Hex != wantHex {
				t.Fatalf("diffID = %s, want %s", diffID.Hex, wantHex)
			}

			// digest should be the sha256 of the compressed blob, differing from diffID when compressed.
			digest, err := layer.Digest()
			if err != nil {
				t.Fatal(err)
			}
			if algo != None && digest.Hex == wantHex {
				t.Fatal("digest should differ from diffID when compressed")
			}

			// Uncompressed should restore the original bytes.
			rc, err := layer.Uncompressed()
			if err != nil {
				t.Fatal(err)
			}
			got, err := io.ReadAll(rc)
			rc.Close()
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, payload) {
				t.Fatalf("uncompressed round-trip mismatch: got %d bytes want %d", len(got), len(payload))
			}
		})
	}
}
