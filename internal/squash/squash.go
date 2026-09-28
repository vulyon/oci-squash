package squash

import (
	"archive/tar"
	"fmt"
	"io"
	"time"

	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/empty"
	"github.com/google/go-containerregistry/pkg/v1/mutate"

	"oci-squash/internal/compress"
)

// Options controls a single squash run.
type Options struct {
	From     string           // see selectLayers
	Message  string           // history comment for the new squashed layer
	TmpDir   string           // temp dir for the compressed blob ("" = system temp dir)
	Compress compress.Options // compression algorithm/level/parallelism
}

// Squash loads img, merges the top layers per opts, and returns a new v1.Image.
// The returned cleanup removes the temporary compressed blob; call it after writing out.
func Squash(img v1.Image, opts Options) (v1.Image, func() error, error) {
	layers, err := img.Layers()
	if err != nil {
		return nil, nil, fmt.Errorf("read layers: %w", err)
	}
	sel, err := selectLayers(layers, opts.From)
	if err != nil {
		return nil, nil, err
	}

	// Collect all paths in the retained lower layers, for whiteout re-emission decisions.
	filesInMove, err := collectPaths(sel.toMove)
	if err != nil {
		return nil, nil, fmt.Errorf("scan move layers: %w", err)
	}

	// Single-pass parallel compression of the merged layer: produce = mergeLayers' output.
	squashLayer, cleanup, err := compress.BuildLayer(opts.TmpDir, opts.Compress, func(w io.Writer) error {
		return mergeLayers(w, sel.toSquash, filesInMove)
	})
	if err != nil {
		return nil, nil, fmt.Errorf("build squashed layer: %w", err)
	}

	newImg, err := assemble(img, sel.toMove, squashLayer, opts.Message)
	if err != nil {
		cleanup()
		return nil, nil, err
	}
	return newImg, cleanup, nil
}

// assemble builds the new image from the retained layers plus the new merged layer, and
// recomputes the config's history/rootfs. Move layers reuse the original v1.Layer (no
// decompress/recompress); diff_ids are rebuilt by mutate.Append.
func assemble(orig v1.Image, moveLayers []v1.Layer, squashLayer v1.Layer, message string) (v1.Image, error) {
	cfg, err := orig.ConfigFile()
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}

	// Start from empty, inherit layer-independent metadata (arch/OS/runtime config),
	// clear rootfs/history (rebuilt by Append), and drop the container snapshot fields.
	base := cfg.DeepCopy()
	base.RootFS = v1.RootFS{Type: "layers"}
	base.History = nil
	base.Container = ""
	base.DockerVersion = ""
	base.Created = v1.Time{Time: time.Now().UTC()}

	img, err := mutate.ConfigFile(empty.Image, base)
	if err != nil {
		return nil, fmt.Errorf("set base config: %w", err)
	}

	// Keep the original history entries that correspond to the move layers
	// (including empty_layer metadata entries).
	moveHistory := selectMoveHistory(cfg.History, len(moveLayers))

	adds := make([]mutate.Addendum, 0, len(moveLayers)+1)
	hi := 0
	for _, l := range moveLayers {
		add := mutate.Addendum{Layer: l}
		// Emit any empty_layer history entries first, in order.
		for hi < len(moveHistory) && moveHistory[hi].EmptyLayer {
			// An empty_layer has no backing layer; append it as a history-only Addendum.
			adds = append(adds, mutate.Addendum{History: moveHistory[hi]})
			hi++
		}
		if hi < len(moveHistory) {
			add.History = moveHistory[hi]
			hi++
		}
		adds = append(adds, add)
	}
	// Append any trailing empty_layer entries.
	for hi < len(moveHistory) {
		if moveHistory[hi].EmptyLayer {
			adds = append(adds, mutate.Addendum{History: moveHistory[hi]})
		}
		hi++
	}

	// History entry for the new merged layer.
	adds = append(adds, mutate.Addendum{
		Layer: squashLayer,
		History: v1.History{
			Created:   base.Created,
			Comment:   message,
			CreatedBy: "oci-squash",
		},
	})

	img, err = mutate.Append(img, adds...)
	if err != nil {
		return nil, fmt.Errorf("append layers: %w", err)
	}
	return img, nil
}

// selectMoveHistory returns the history entries for the first nMove non-empty layers,
// preserving any interleaved empty_layer entries in order.
func selectMoveHistory(history []v1.History, nMove int) []v1.History {
	if nMove == 0 {
		return nil
	}
	var out []v1.History
	nonEmpty := 0
	for _, h := range history {
		out = append(out, h)
		if !h.EmptyLayer {
			nonEmpty++
			if nonEmpty == nMove {
				break
			}
		}
	}
	return out
}

// collectPaths reads all normalized paths present in the given layers.
func collectPaths(layers []v1.Layer) (map[string]struct{}, error) {
	set := make(map[string]struct{})
	for _, l := range layers {
		rc, err := l.Uncompressed()
		if err != nil {
			return nil, err
		}
		tr := tar.NewReader(rc)
		for {
			hdr, err := tr.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				rc.Close()
				return nil, err
			}
			set[normalize(hdr.Name)] = struct{}{}
		}
		rc.Close()
	}
	return set, nil
}
