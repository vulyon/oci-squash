package squash

import (
	"archive/tar"
	"fmt"
	"io"
	"sort"

	v1 "github.com/google/go-containerregistry/pkg/v1"
)

// mergeLayers merges layersToSquash into a single uncompressed tar stream written to w.
//
// Algorithm (mirrors the Python docker-squash _squash_layers, but replaces list-based
// linear lookups with maps/prefix sets and per-byte copies with io.Copy):
//
//   - Iterate layers newest-first.
//   - For each layer, first scan whiteout / opaque markers, then process regular entries.
//   - "Last write wins": a path already written by a newer layer is skipped in older layers.
//   - Paths hit by a whiteout, or under an opaque directory, are skipped in older layers.
//   - Symlinks / hardlinks are deferred and written at the end (broken symlinks allowed).
//
// filesInLayersToMove is the set of all paths present in the retained lower layers. If a
// squashed whiteout deletes a target that still exists in a lower layer, the whiteout marker
// must be re-emitted into the merged layer, otherwise the lower-layer file would not be hidden
// (mirrors the Python _add_markers).
func mergeLayers(w io.Writer, layersToSquash []v1.Layer, filesInLayersToMove map[string]struct{}) error {
	tw := tar.NewWriter(w)

	squashed := make(map[string]struct{}) // paths already written
	skip := newPathSet()                   // paths hit by a whiteout; skip in older layers
	opaque := newPrefixSet()               // opaque directory prefixes

	// Deferred links: preserve appearance order, but keep the newest layer's version per name.
	symlinks := make(map[string]deferredLink)
	hardlinks := make(map[string]deferredLink)

	// Whiteout markers that must be re-emitted (deleted target still exists in a lower layer).
	reemit := make(map[string]*tar.Header)

	order := 0

	for i := len(layersToSquash) - 1; i >= 0; i-- {
		layer := layersToSquash[i]
		rc, err := layer.Uncompressed()
		if err != nil {
			return fmt.Errorf("read uncompressed layer: %w", err)
		}
		tr := tar.NewReader(rc)

		// Whiteout / opaque markers introduced by this layer; applied to *older* layers.
		// A whiteout must not mask this layer's own files, so merge into the global sets
		// only after finishing this layer.
		layerSkip := newPathSet()
		layerOpaque := newPrefixSet()

		for {
			hdr, err := tr.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				rc.Close()
				return fmt.Errorf("read tar entry: %w", err)
			}

			name := normalize(hdr.Name)

			switch {
			case isOpaque(hdr.Name):
				dir := opaqueDir(hdr.Name)
				layerOpaque.add(dir)
				continue
			case isWhiteout(hdr.Name):
				target := whiteoutTarget(hdr.Name)
				layerSkip.add(target)
				// If the deleted target exists in a retained lower layer, re-emit the marker.
				if _, ok := filesInLayersToMove[target]; ok {
					reemit[normalize(hdr.Name)] = cloneHeader(hdr)
				}
				continue
			}

			// Masked by a newer layer's whiteout / opaque -> skip.
			if skip.has(name) || opaque.covers(name) {
				continue
			}
			// Last write wins: a newer layer already wrote this path.
			if _, ok := squashed[name]; ok {
				continue
			}

			switch hdr.Typeflag {
			case tar.TypeSymlink:
				if _, ok := symlinks[name]; !ok {
					symlinks[name] = deferredLink{hdr: cloneHeader(hdr), order: order}
					order++
				}
				squashed[name] = struct{}{}
				continue
			case tar.TypeLink:
				if _, ok := hardlinks[name]; !ok {
					hardlinks[name] = deferredLink{hdr: cloneHeader(hdr), order: order}
					order++
				}
				squashed[name] = struct{}{}
				continue
			}

			if err := tw.WriteHeader(hdr); err != nil {
				rc.Close()
				return fmt.Errorf("write header %s: %w", name, err)
			}
			if hdr.Typeflag == tar.TypeReg {
				if _, err := io.Copy(tw, tr); err != nil {
					rc.Close()
					return fmt.Errorf("copy %s: %w", name, err)
				}
			}
			squashed[name] = struct{}{}
		}
		rc.Close()

		// Merge this layer's whiteout/opaque sets into the global ones for older layers.
		for p := range layerSkip.m {
			skip.add(p)
		}
		for d := range layerOpaque.dirs {
			opaque.add(d)
		}
	}

	// Write deferred links, preserving deterministic appearance order.
	if err := writeDeferred(tw, symlinks, hardlinks, squashed); err != nil {
		return err
	}

	// Re-emit whiteout markers needed to mask lower layers (after marker reduction).
	if err := writeReemitMarkers(tw, reemit); err != nil {
		return err
	}

	return tw.Close()
}

// deferredLink is a link entry written at the end; order keeps output deterministic.
type deferredLink struct {
	hdr   *tar.Header
	order int
}

// writeDeferred writes deferred symlinks / hardlinks. A hardlink whose target was not written
// is dropped (matching the Python behavior); symlinks are allowed to dangle.
func writeDeferred(tw *tar.Writer, symlinks, hardlinks map[string]deferredLink, squashed map[string]struct{}) error {
	items := make([]deferredLink, 0, len(symlinks)+len(hardlinks))
	for _, d := range symlinks {
		items = append(items, d)
	}
	for _, d := range hardlinks {
		// The hardlink target must already be written, otherwise skip it.
		if _, ok := squashed[normalize(d.hdr.Linkname)]; !ok {
			continue
		}
		items = append(items, d)
	}
	sort.Slice(items, func(a, b int) bool { return items[a].order < items[b].order })
	for _, it := range items {
		if err := tw.WriteHeader(it.hdr); err != nil {
			return fmt.Errorf("write deferred link %s: %w", it.hdr.Name, err)
		}
	}
	return nil
}

// writeReemitMarkers writes re-emitted whiteout markers and performs marker reduction:
// if a marker's ancestor directory is itself an opaque/whiteout marker, the marker is
// redundant and omitted (mirrors the Python _reduce -- redundant markers can break image load).
func writeReemitMarkers(tw *tar.Writer, reemit map[string]*tar.Header) error {
	covered := func(markerPath string) bool {
		for _, anc := range pathHierarchy(markerPath) {
			// An opaque marker path at an ancestor directory.
			if _, ok := reemit[normalize(anc+"/"+opaqueMarker)]; ok {
				return true
			}
		}
		return false
	}
	for markerPath, hdr := range reemit {
		if covered(markerPath) {
			continue
		}
		if err := tw.WriteHeader(hdr); err != nil {
			return fmt.Errorf("write reemit marker %s: %w", markerPath, err)
		}
	}
	return nil
}

// cloneHeader deep-copies a tar.Header to avoid retaining the tar.Reader's reused pointers.
func cloneHeader(h *tar.Header) *tar.Header {
	c := *h
	if h.PAXRecords != nil {
		c.PAXRecords = make(map[string]string, len(h.PAXRecords))
		for k, v := range h.PAXRecords {
			c.PAXRecords[k] = v
		}
	}
	if h.Xattrs != nil { //nolint:staticcheck // kept for compatibility with older tar headers
		c.Xattrs = make(map[string]string, len(h.Xattrs))
		for k, v := range h.Xattrs {
			c.Xattrs[k] = v
		}
	}
	return &c
}
