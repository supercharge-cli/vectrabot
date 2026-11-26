package main

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestDiffTreeReportsAddedModifiedDeleted(t *testing.T) {
	root := t.TempDir()
	write := func(name, body string) {
		t.Helper()
		path := filepath.Join(root, name)
		_ = os.MkdirAll(filepath.Dir(path), 0o755)
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("keep.txt", "same")
	write("edit.txt", "old")
	write("gone.txt", "bye")
	write("node_modules/pkg/index.js", "ignored")
	before := snapshotTree(root)
	write("edit.txt", "new body")
	later := time.Now().Add(2 * time.Second)
	_ = os.Chtimes(filepath.Join(root, "edit.txt"), later, later)
	write("src/new.go", "package x")
	write("node_modules/pkg/other.js", "ignored too")
	_ = os.Remove(filepath.Join(root, "gone.txt"))
	got := diffTree(before, snapshotTree(root))
	want := []fileChange{{"edit.txt", "modified"}, {"gone.txt", "deleted"}, {"src/new.go", "added"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("changes %+v", got)
	}
	if diffTree(nil, before) != nil {
		t.Fatal("an unknown snapshot must report no list")
	}
}
