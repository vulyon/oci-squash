package squash

import (
	"archive/tar"
	"bytes"
	"io"
	"testing"

	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/empty"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/tarball"

	"oci-squash/internal/compress"
)

// tarballLayer wraps entries into a real v1.Layer that can compute diff_id/digest.
func tarballLayer(t *testing.T, entries ...entry) v1.Layer {
	t.Helper()
	ml := makeLayer(entries...)
	l, err := tarball.LayerFromReader(bytes.NewReader(ml.data))
	if err != nil {
		t.Fatalf("build tarball layer: %v", err)
	}
	return l
}

// buildImage assembles a test image from the given layers, base to top.
func buildImage(t *testing.T, layers ...v1.Layer) v1.Image {
	t.Helper()
	img := empty.Image
	for _, l := range layers {
		var err error
		img, err = mutate.AppendLayers(img, l)
		if err != nil {
			t.Fatalf("append layer: %v", err)
		}
	}
	return img
}

// imageFiles resolves the merged filesystem across all layers (applying whiteout semantics).
func imageFiles(t *testing.T, img v1.Image) map[string]string {
	t.Helper()
	layers, err := img.Layers()
	if err != nil {
		t.Fatal(err)
	}
	files := map[string]string{}
	for _, l := range layers {
		rc, err := l.Uncompressed()
		if err != nil {
			t.Fatal(err)
		}
		tr := tar.NewReader(rc)
		for {
			hdr, err := tr.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Fatal(err)
			}
			name := normalize(hdr.Name)
			if isWhiteout(hdr.Name) {
				delete(files, whiteoutTarget(hdr.Name))
				continue
			}
			body, _ := io.ReadAll(tr)
			files[name] = string(body)
		}
		rc.Close()
	}
	return files
}

// TestSquash_EndToEnd_All squashes all layers and verifies: only 1 layer remains, the
// filesystem is correct, and the new image's config is consistent (layers == diff_ids).
func TestSquash_EndToEnd_All(t *testing.T) {
	img := buildImage(t,
		tarballLayer(t, entry{name: "a.txt", body: "1"}),
		tarballLayer(t, entry{name: "b.txt", body: "2"}, entry{name: ".wh.a.txt"}), // delete a.txt
		tarballLayer(t, entry{name: "c.txt", body: "3"}),
	)

	out, cleanup, err := Squash(img, Options{
		Message:  "squashed",
		TmpDir:   t.TempDir(),
		Compress: compress.Options{Algorithm: compress.Gzip},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()

	layers, err := out.Layers()
	if err != nil {
		t.Fatal(err)
	}
	if len(layers) != 1 {
		t.Fatalf("want 1 layer after squashing all, got %d", len(layers))
	}

	files := imageFiles(t, out)
	if _, ok := files["/a.txt"]; ok {
		t.Error("a.txt should be removed by whiteout")
	}
	if files["/b.txt"] != "2" || files["/c.txt"] != "3" {
		t.Errorf("unexpected files: %v", files)
	}

	assertConfigConsistent(t, out)
}

// TestSquash_EndToEnd_LastN squashes only the top 2 layers; the base layer is retained.
func TestSquash_EndToEnd_LastN(t *testing.T) {
	img := buildImage(t,
		tarballLayer(t, entry{name: "base.txt", body: "base"}),
		tarballLayer(t, entry{name: "mid.txt", body: "mid"}),
		tarballLayer(t, entry{name: "top.txt", body: "top"}),
	)

	out, cleanup, err := Squash(img, Options{
		From:     "2",
		TmpDir:   t.TempDir(),
		Compress: compress.Options{Algorithm: compress.Zstd},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()

	layers, err := out.Layers()
	if err != nil {
		t.Fatal(err)
	}
	// 1 retained layer + 1 merged layer = 2.
	if len(layers) != 2 {
		t.Fatalf("want 2 layers (1 move + 1 squashed), got %d", len(layers))
	}

	files := imageFiles(t, out)
	for _, f := range []string{"/base.txt", "/mid.txt", "/top.txt"} {
		if _, ok := files[f]; !ok {
			t.Errorf("missing %s in result: %v", f, files)
		}
	}
	assertConfigConsistent(t, out)
}

// TestSquash_WhiteoutAcrossMoveBoundary: the deleted target is in the retained lower layer,
// so the squashed layer must re-emit the whiteout, keeping the lower-layer file hidden.
func TestSquash_WhiteoutAcrossMoveBoundary(t *testing.T) {
	img := buildImage(t,
		tarballLayer(t, entry{name: "keep.txt", body: "k"}, entry{name: "gone.txt", body: "g"}),
		tarballLayer(t, entry{name: ".wh.gone.txt"}), // top layer deletes the base's gone.txt
	)

	// from=1 squashes only 1 layer, which should report nothing to squash.
	if _, _, err := Squash(img, Options{
		From:     "1",
		TmpDir:   t.TempDir(),
		Compress: compress.Options{Algorithm: compress.Gzip},
	}); err == nil {
		t.Fatal("expected ErrNothingToSquash for from=1")
	}

	// Squash both layers: gone.txt is deleted, keep.txt survives.
	out, cleanup, err := Squash(img, Options{
		TmpDir:   t.TempDir(),
		Compress: compress.Options{Algorithm: compress.Gzip},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()

	files := imageFiles(t, out)
	if _, ok := files["/gone.txt"]; ok {
		t.Error("gone.txt should be deleted")
	}
	if files["/keep.txt"] != "k" {
		t.Error("keep.txt should survive")
	}
}

// assertConfigConsistent checks that rootfs.diff_ids count matches the layer count.
func assertConfigConsistent(t *testing.T, img v1.Image) {
	t.Helper()
	cfg, err := img.ConfigFile()
	if err != nil {
		t.Fatal(err)
	}
	layers, err := img.Layers()
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.RootFS.DiffIDs) != len(layers) {
		t.Fatalf("diff_ids=%d != layers=%d", len(cfg.RootFS.DiffIDs), len(layers))
	}
	// Each layer's DiffID should match the config record.
	for i, l := range layers {
		d, err := l.DiffID()
		if err != nil {
			t.Fatal(err)
		}
		if d != cfg.RootFS.DiffIDs[i] {
			t.Errorf("layer %d diff_id mismatch: %s vs %s", i, d, cfg.RootFS.DiffIDs[i])
		}
	}
}
