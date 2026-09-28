package squash

import (
	"errors"
	"testing"

	v1 "github.com/google/go-containerregistry/pkg/v1"
)

func nLayers(n int) []v1.Layer {
	ls := make([]v1.Layer, n)
	for i := range ls {
		ls[i] = makeLayer(entry{name: "f", body: "x"})
	}
	return ls
}

func TestSelectLayers_All(t *testing.T) {
	sel, err := selectLayers(nLayers(4), "")
	if err != nil {
		t.Fatal(err)
	}
	if len(sel.toMove) != 0 || len(sel.toSquash) != 4 {
		t.Fatalf("all: move=%d squash=%d, want 0/4", len(sel.toMove), len(sel.toSquash))
	}
}

func TestSelectLayers_LastN(t *testing.T) {
	sel, err := selectLayers(nLayers(5), "2")
	if err != nil {
		t.Fatal(err)
	}
	if len(sel.toMove) != 3 || len(sel.toSquash) != 2 {
		t.Fatalf("lastN: move=%d squash=%d, want 3/2", len(sel.toMove), len(sel.toSquash))
	}
}

func TestSelectLayers_Errors(t *testing.T) {
	if _, err := selectLayers(nil, ""); err == nil {
		t.Error("empty image should error")
	}
	if _, err := selectLayers(nLayers(3), "0"); err == nil {
		t.Error("N=0 should error")
	}
	if _, err := selectLayers(nLayers(3), "-1"); err == nil {
		t.Error("negative N should error")
	}
	if _, err := selectLayers(nLayers(3), "5"); err == nil {
		t.Error("N>total should error")
	}
	if _, err := selectLayers(nLayers(3), "abc"); err == nil {
		t.Error("non-integer from should error")
	}
}

func TestSelectLayers_NothingToSquash(t *testing.T) {
	// Only 1 squashable layer -> ErrNothingToSquash.
	_, err := selectLayers(nLayers(3), "1")
	if !errors.Is(err, ErrNothingToSquash) {
		t.Fatalf("want ErrNothingToSquash, got %v", err)
	}
	// A single-layer image -> nothing to squash either.
	_, err = selectLayers(nLayers(1), "")
	if !errors.Is(err, ErrNothingToSquash) {
		t.Fatalf("single-layer image: want ErrNothingToSquash, got %v", err)
	}
}
