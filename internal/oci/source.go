// Package oci loads OCI/Docker images from various sources into ggcr's v1.Image and writes
// squash results as an OCI layout or a docker-archive.
package oci

import (
	"fmt"
	"strings"

	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/layout"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/tarball"
)

// LoadImage loads an image from a source string and normalizes it to a v1.Image. Supports:
//
//	oci:PATH              OCI layout directory
//	oci-archive:FILE.tar  OCI archive tar
//	docker-archive:FILE   docker save output
//	docker-daemon:REF     (reserved, not yet implemented)
//	anything else         treated as a registry reference and pulled remotely
func LoadImage(src string) (v1.Image, error) {
	scheme, rest, hasScheme := strings.Cut(src, ":")
	if !hasScheme {
		return loadRemote(src)
	}
	switch scheme {
	case "oci":
		return loadOCILayout(rest)
	case "oci-archive":
		return loadOCIArchive(rest)
	case "docker-archive":
		return loadDockerArchive(rest)
	default:
		// A reference like registry.io/repo:tag contains ":" but is not a scheme;
		// hand it to the remote resolver.
		return loadRemote(src)
	}
}

// loadOCILayout reads the sole (or first) image manifest from an OCI layout directory.
func loadOCILayout(path string) (v1.Image, error) {
	p, err := layout.FromPath(path)
	if err != nil {
		return nil, fmt.Errorf("open oci layout %q: %w", path, err)
	}
	idx, err := p.ImageIndex()
	if err != nil {
		return nil, fmt.Errorf("read index: %w", err)
	}
	return imageFromIndex(idx)
}

func loadOCIArchive(file string) (v1.Image, error) {
	idx, err := layout.ImageIndexFromPath(file)
	if err != nil {
		return nil, fmt.Errorf("open oci archive %q: %w", file, err)
	}
	return imageFromIndex(idx)
}

func loadDockerArchive(file string) (v1.Image, error) {
	img, err := tarball.ImageFromPath(file, nil)
	if err != nil {
		return nil, fmt.Errorf("open docker archive %q: %w", file, err)
	}
	return img, nil
}

func loadRemote(ref string) (v1.Image, error) {
	r, err := name.ParseReference(ref)
	if err != nil {
		return nil, fmt.Errorf("parse reference %q: %w", ref, err)
	}
	img, err := remote.Image(r, remote.WithAuthFromKeychain(nil))
	if err != nil {
		return nil, fmt.Errorf("pull %q: %w", ref, err)
	}
	return img, nil
}

// imageFromIndex extracts a single image from an index. If it contains multiple manifests,
// the first image-type manifest is used.
func imageFromIndex(idx v1.ImageIndex) (v1.Image, error) {
	manifest, err := idx.IndexManifest()
	if err != nil {
		return nil, fmt.Errorf("read index manifest: %w", err)
	}
	for _, desc := range manifest.Manifests {
		if desc.MediaType.IsImage() {
			return idx.Image(desc.Digest)
		}
	}
	// Fallback: a single entry whose media type is not marked as an image.
	if len(manifest.Manifests) == 1 {
		return idx.Image(manifest.Manifests[0].Digest)
	}
	return nil, fmt.Errorf("no image manifest found in index (%d manifests)", len(manifest.Manifests))
}
