package squash

import (
	"path"
	"strings"
)

// OCI/overlayfs and AUFS whiteout conventions:
//   - regular deletion: a .wh.<name> file in the same directory deletes <name> in a lower layer
//   - opaque directory: a .wh..wh..opq file masks all lower-layer content under that directory
const (
	whiteoutPrefix = ".wh."
	opaqueMarker   = ".wh..wh..opq"
)

// isOpaque reports whether a tar entry is an opaque directory marker.
func isOpaque(name string) bool {
	return path.Base(name) == opaqueMarker
}

// isWhiteout reports whether a tar entry is a regular whiteout marker (excluding opaque).
func isWhiteout(name string) bool {
	base := path.Base(name)
	return strings.HasPrefix(base, whiteoutPrefix) && base != opaqueMarker
}

// whiteoutTarget returns the (normalized) path that a regular whiteout marker deletes.
// e.g. /usr/.wh.foo -> /usr/foo
func whiteoutTarget(name string) string {
	dir := path.Dir(name)
	base := path.Base(name)
	target := strings.TrimPrefix(base, whiteoutPrefix)
	return normalize(path.Join(dir, target))
}

// opaqueDir returns the (normalized) directory an opaque marker applies to.
func opaqueDir(name string) string {
	return normalize(path.Dir(name))
}

// normalize canonicalizes a tar entry path to a clean absolute path starting with "/",
// eliminating the "./"-prefix vs bare-path inconsistency (mirrors the Python _normalize_path).
func normalize(name string) string {
	return path.Clean("/" + name)
}

// pathSet records paths hit by a whiteout that must be skipped in subsequent (older) layers.
type pathSet struct {
	m map[string]struct{}
}

func newPathSet() *pathSet { return &pathSet{m: make(map[string]struct{})} }

func (s *pathSet) add(p string)      { s.m[p] = struct{}{} }
func (s *pathSet) has(p string) bool { _, ok := s.m[p]; return ok }

// prefixSet records opaque directory prefixes; covers reports whether a path lies under any.
type prefixSet struct {
	dirs map[string]struct{}
}

func newPrefixSet() *prefixSet { return &prefixSet{dirs: make(map[string]struct{})} }

func (s *prefixSet) add(dir string) { s.dirs[dir] = struct{}{} }

// covers reports whether name lies *under* some opaque directory (excluding the dir node itself).
// An opaque marker masks lower-layer content inside the directory, not the directory entry.
func (s *prefixSet) covers(name string) bool {
	for dir := range s.dirs {
		if dir == "/" {
			if name != "/" {
				return true
			}
			continue
		}
		if strings.HasPrefix(name, dir+"/") {
			return true
		}
	}
	return false
}

// pathHierarchy returns all ancestor directories of an absolute path (excluding itself,
// including "/"), deepest first. Used for marker reduction: deciding whether a whiteout is
// already covered by an ancestor directory marker. Mirrors the Python _path_hierarchy.
func pathHierarchy(p string) []string {
	p = normalize(p)
	var out []string
	for p != "/" {
		parent := path.Dir(p)
		out = append(out, parent)
		p = parent
	}
	return out
}
