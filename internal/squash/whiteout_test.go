package squash

import (
	"reflect"
	"testing"
)

func TestNormalize(t *testing.T) {
	cases := map[string]string{
		"a.txt":        "/a.txt",
		"./a.txt":      "/a.txt",
		"/a.txt":       "/a.txt",
		"dir/../a.txt": "/a.txt",
		"dir/./b":      "/dir/b",
		"./":           "/",
		"":             "/",
	}
	for in, want := range cases {
		if got := normalize(in); got != want {
			t.Errorf("normalize(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestIsWhiteoutAndOpaque(t *testing.T) {
	if !isOpaque("data/.wh..wh..opq") {
		t.Error("should detect opaque marker")
	}
	if isOpaque("data/.wh.foo") {
		t.Error("regular whiteout is not opaque")
	}
	if !isWhiteout("dir/.wh.foo") {
		t.Error("should detect regular whiteout")
	}
	if isWhiteout("data/.wh..wh..opq") {
		t.Error("opaque must not be classified as regular whiteout")
	}
	if isWhiteout("normal.txt") {
		t.Error("normal file is not whiteout")
	}
}

func TestWhiteoutTarget(t *testing.T) {
	cases := map[string]string{
		"usr/.wh.foo":   "/usr/foo",
		".wh.a.txt":     "/a.txt",
		"a/b/.wh.c.txt": "/a/b/c.txt",
		"./x/.wh.y":     "/x/y",
	}
	for in, want := range cases {
		if got := whiteoutTarget(in); got != want {
			t.Errorf("whiteoutTarget(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestOpaqueDir(t *testing.T) {
	if got := opaqueDir("data/sub/.wh..wh..opq"); got != "/data/sub" {
		t.Errorf("opaqueDir = %q, want /data/sub", got)
	}
	if got := opaqueDir(".wh..wh..opq"); got != "/" {
		t.Errorf("opaqueDir root = %q, want /", got)
	}
}

func TestPrefixSetCovers(t *testing.T) {
	ps := newPrefixSet()
	ps.add("/data")
	if !ps.covers("/data/x.txt") {
		t.Error("should cover file under opaque dir")
	}
	if !ps.covers("/data/sub/y") {
		t.Error("should cover nested file under opaque dir")
	}
	if ps.covers("/data") {
		t.Error("must NOT cover the opaque dir node itself")
	}
	if ps.covers("/database") {
		t.Error("must not cover sibling with shared prefix")
	}
	if ps.covers("/other") {
		t.Error("must not cover unrelated path")
	}
}

func TestPrefixSetCoversRoot(t *testing.T) {
	ps := newPrefixSet()
	ps.add("/")
	if !ps.covers("/anything") {
		t.Error("root opaque should cover everything below")
	}
	if ps.covers("/") {
		t.Error("root opaque should not cover root node itself")
	}
}

func TestPathHierarchy(t *testing.T) {
	got := pathHierarchy("/a/b/c.txt")
	want := []string{"/a/b", "/a", "/"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("pathHierarchy = %v, want %v", got, want)
	}
	if h := pathHierarchy("/"); len(h) != 0 {
		t.Errorf("root hierarchy should be empty, got %v", h)
	}
}

func TestPathSet(t *testing.T) {
	s := newPathSet()
	s.add("/x")
	if !s.has("/x") {
		t.Error("should contain added path")
	}
	if s.has("/y") {
		t.Error("should not contain missing path")
	}
}
