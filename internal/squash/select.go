package squash

import (
	"fmt"
	"strconv"

	v1 "github.com/google/go-containerregistry/pkg/v1"
)

// selection describes how layers are split: bottom "move" layers are retained,
// top "squash" layers are merged into one.
type selection struct {
	toMove   []v1.Layer
	toSquash []v1.Layer
}

// selectLayers splits the image layers (ordered base->top) according to from.
//
// from semantics (mirrors the Python docker-squash --from-layer):
//   - ""            squash all layers
//   - integer N     squash the last N layers (the N layers closest to the top)
//   - anything else locating by digest/reference is not yet supported (needs image context)
//
// Guards: N<=0 or N>total is an error; fewer than 2 squashable layers returns ErrNothingToSquash.
func selectLayers(layers []v1.Layer, from string) (*selection, error) {
	total := len(layers)
	if total == 0 {
		return nil, fmt.Errorf("image has no layers")
	}

	var n int
	if from == "" {
		n = total
	} else {
		parsed, err := strconv.Atoi(from)
		if err != nil {
			return nil, fmt.Errorf("--from %q: only an empty value or integer N is supported (locating by layer reference is not yet implemented)", from)
		}
		n = parsed
	}

	if n <= 0 {
		return nil, fmt.Errorf("--from layer count must be positive, got %d", n)
	}
	if n > total {
		return nil, fmt.Errorf("--from layer count %d exceeds the image's total of %d layers", n, total)
	}
	if n < 2 {
		return nil, ErrNothingToSquash
	}

	marker := total - n
	return &selection{
		toMove:   layers[:marker],
		toSquash: layers[marker:],
	}, nil
}

// ErrNothingToSquash indicates fewer than 2 squashable layers, so nothing needs squashing
// (mirrors the Python SquashUnnecessaryError).
var ErrNothingToSquash = fmt.Errorf("fewer than 2 layers to squash; nothing to do")
