package oci

import (
	"fmt"

	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/empty"
	"github.com/google/go-containerregistry/pkg/v1/layout"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/tarball"
)

// OutputFormat is a supported output format.
type OutputFormat string

const (
	FormatOCI           OutputFormat = "oci"            // OCI layout directory
	FormatOCIArchive    OutputFormat = "oci-archive"    // OCI layout packed as a tar (currently same as directory)
	FormatDockerArchive OutputFormat = "docker-archive" // readable by docker load
)

// ParseFormat parses the --format value.
func ParseFormat(s string) (OutputFormat, error) {
	switch OutputFormat(s) {
	case FormatOCI, FormatOCIArchive, FormatDockerArchive:
		return OutputFormat(s), nil
	default:
		return "", fmt.Errorf("unknown format %q (want oci|oci-archive|docker-archive)", s)
	}
}

// WriteImage writes the squashed image to path in the given format. tag may be empty; when
// non-empty it is used as the RepoTags (docker-archive) or an index annotation.
func WriteImage(path string, img v1.Image, format OutputFormat, tag string) error {
	switch format {
	case FormatOCI, FormatOCIArchive:
		return writeOCILayout(path, img, tag)
	case FormatDockerArchive:
		return writeDockerArchive(path, img, tag)
	default:
		return fmt.Errorf("unhandled format %q", format)
	}
}

func writeOCILayout(path string, img v1.Image, tag string) error {
	add := mutate.IndexAddendum{Add: img}
	if tag != "" {
		if ref, err := name.ParseReference(tag); err == nil {
			add.Annotations = map[string]string{
				"org.opencontainers.image.ref.name": ref.Name(),
			}
		}
	}
	idx := mutate.AppendManifests(empty.Index, add)
	if _, err := layout.Write(path, idx); err != nil {
		return fmt.Errorf("write oci layout: %w", err)
	}
	return nil
}

func writeDockerArchive(path string, img v1.Image, tag string) error {
	var ref name.Reference
	if tag != "" {
		r, err := name.NewTag(tag)
		if err != nil {
			return fmt.Errorf("parse tag %q: %w", tag, err)
		}
		ref = r
	} else {
		r, _ := name.NewTag("squashed:latest")
		ref = r
	}
	if err := tarball.WriteToFile(path, ref, img); err != nil {
		return fmt.Errorf("write docker archive: %w", err)
	}
	return nil
}
