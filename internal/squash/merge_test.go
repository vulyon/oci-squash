package squash

import (
	"archive/tar"
	"bytes"
	"io"
	"testing"

	v1 "github.com/google/go-containerregistry/pkg/v1"
	v1types "github.com/google/go-containerregistry/pkg/v1/types"
)

// memLayer is a minimal v1.Layer implementing only Uncompressed, for feeding mergeLayers.
type memLayer struct{ data []byte }

var _ v1.Layer = (*memLayer)(nil)

func (m *memLayer) Uncompressed() (io.ReadCloser, error) {
	return io.NopCloser(bytes.NewReader(m.data)), nil
}
func (m *memLayer) Compressed() (io.ReadCloser, error)    { return m.Uncompressed() }
func (m *memLayer) Digest() (v1.Hash, error)              { return v1.Hash{}, nil }
func (m *memLayer) DiffID() (v1.Hash, error)              { return v1.Hash{}, nil }
func (m *memLayer) Size() (int64, error)                  { return int64(len(m.data)), nil }
func (m *memLayer) MediaType() (v1types.MediaType, error) { return v1types.OCILayer, nil }

type entry struct {
	name     string
	typeflag byte
	body     string
	linkname string
}

func makeLayer(entries ...entry) *memLayer {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for _, e := range entries {
		tf := e.typeflag
		if tf == 0 {
			tf = tar.TypeReg
		}
		hdr := &tar.Header{
			Name:     e.name,
			Typeflag: tf,
			Linkname: e.linkname,
			Mode:     0o644,
			Size:     int64(len(e.body)),
		}
		if err := tw.WriteHeader(hdr); err != nil {
			panic(err)
		}
		if len(e.body) > 0 {
			tw.Write([]byte(e.body))
		}
	}
	tw.Close()
	return &memLayer{data: buf.Bytes()}
}

// mergeToMap runs mergeLayers and parses the result tar into a name->body map.
func mergeToMap(t *testing.T, layers []v1.Layer, filesInMove map[string]struct{}) map[string]string {
	t.Helper()
	var out bytes.Buffer
	if err := mergeLayers(&out, layers, filesInMove); err != nil {
		t.Fatalf("mergeLayers: %v", err)
	}
	got := map[string]string{}
	tr := tar.NewReader(&out)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("read result: %v", err)
		}
		body, _ := io.ReadAll(tr)
		got[normalize(hdr.Name)] = string(body)
	}
	return got
}

func TestMerge_LastWriteWins(t *testing.T) {
	// Old layer a=old, newer layer a=new; new should win.
	old := makeLayer(entry{name: "a.txt", body: "old"})
	newer := makeLayer(entry{name: "a.txt", body: "new"})
	got := mergeToMap(t, []v1.Layer{old, newer}, nil)
	if got["/a.txt"] != "new" {
		t.Fatalf("want new, got %q", got["/a.txt"])
	}
}

func TestMerge_Whiteout(t *testing.T) {
	// Old layer has a.txt; newer layer deletes it with .wh.a.txt.
	old := makeLayer(entry{name: "a.txt", body: "x"}, entry{name: "b.txt", body: "y"})
	newer := makeLayer(entry{name: ".wh.a.txt", body: ""})
	got := mergeToMap(t, []v1.Layer{old, newer}, nil)
	if _, ok := got["/a.txt"]; ok {
		t.Fatal("a.txt should be deleted by whiteout")
	}
	if got["/b.txt"] != "y" {
		t.Fatal("b.txt should survive")
	}
	// The whiteout marker itself should not appear in the merged layer
	// (its target is not in a move layer).
	if _, ok := got["/.wh.a.txt"]; ok {
		t.Fatal("whiteout marker should not be emitted when target not in move layers")
	}
}

func TestMerge_OpaqueDir(t *testing.T) {
	// Old layer has files under data/; newer layer marks data/ opaque and adds a new file.
	old := makeLayer(
		entry{name: "data/old1.txt", body: "1"},
		entry{name: "data/old2.txt", body: "2"},
	)
	newer := makeLayer(
		entry{name: "data/.wh..wh..opq", body: ""},
		entry{name: "data/new.txt", body: "n"},
	)
	got := mergeToMap(t, []v1.Layer{old, newer}, nil)
	if _, ok := got["/data/old1.txt"]; ok {
		t.Fatal("old1 should be masked by opaque dir")
	}
	if _, ok := got["/data/old2.txt"]; ok {
		t.Fatal("old2 should be masked by opaque dir")
	}
	if got["/data/new.txt"] != "n" {
		t.Fatal("new.txt should survive")
	}
}

func TestMerge_WhiteoutReemitForMoveLayer(t *testing.T) {
	// The deleted target exists in a lower (move) layer, so the whiteout must be re-emitted.
	newer := makeLayer(entry{name: ".wh.lower.txt", body: ""})
	filesInMove := map[string]struct{}{"/lower.txt": {}}
	got := mergeToMap(t, []v1.Layer{newer}, filesInMove)
	if _, ok := got["/.wh.lower.txt"]; !ok {
		t.Fatal("whiteout marker should be re-emitted when target is in a move layer")
	}
}

func TestMerge_Symlink(t *testing.T) {
	old := makeLayer(entry{name: "target.txt", body: "t"})
	newer := makeLayer(entry{name: "link", typeflag: tar.TypeSymlink, linkname: "target.txt"})
	var out bytes.Buffer
	if err := mergeLayers(&out, []v1.Layer{old, newer}, nil); err != nil {
		t.Fatalf("mergeLayers: %v", err)
	}
	tr := tar.NewReader(&out)
	foundLink := false
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if hdr.Typeflag == tar.TypeSymlink && normalize(hdr.Name) == "/link" {
			foundLink = true
			if hdr.Linkname != "target.txt" {
				t.Fatalf("bad linkname %q", hdr.Linkname)
			}
		}
	}
	if !foundLink {
		t.Fatal("symlink should be present in merged layer")
	}
}
