package main

import (
	"io/fs"
	"path/filepath"
	"sort"
	"time"
)

// A turn reports which workspace files changed while it ran, so the apps can
// show "edited 2 files" under the reply. It compares two cheap stat walks.

type fileStamp struct {
	size    int64
	modTime time.Time
}

type fileChange struct {
	Path string `json:"path"`
	Kind string `json:"kind"` // added, modified, deleted
}

const (
	snapshotLimit = 50_000
	changeLimit   = 200
)

var skippedDirs = map[string]bool{
	".git": true, ".vectrabot": true, "node_modules": true, ".venv": true, "venv": true, "__pycache__": true,
	".cache": true, ".next": true, ".turbo": true, "target": true, ".gradle": true,
}

// snapshotTree stats every file under root; nil means the tree was too large
// to compare, and no change list is reported.
func snapshotTree(root string) map[string]fileStamp {
	files := map[string]fileStamp{}
	tooMany := false
	_ = filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if entry.IsDir() {
			if path != root && skippedDirs[entry.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if !entry.Type().IsRegular() {
			return nil
		}
		if len(files) >= snapshotLimit {
			tooMany = true
			return filepath.SkipAll
		}
		info, err := entry.Info()
		if err != nil {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return nil
		}
		files[filepath.ToSlash(rel)] = fileStamp{size: info.Size(), modTime: info.ModTime()}
		return nil
	})
	if tooMany {
		return nil
	}
	return files
}

func diffTree(before, after map[string]fileStamp) []fileChange {
	if before == nil || after == nil {
		return nil
	}
	changes := []fileChange{}
	for path, now := range after {
		was, ok := before[path]
		switch {
		case !ok:
			changes = append(changes, fileChange{path, "added"})
		case was.size != now.size || !was.modTime.Equal(now.modTime):
			changes = append(changes, fileChange{path, "modified"})
		}
	}
	for path := range before {
		if _, ok := after[path]; !ok {
			changes = append(changes, fileChange{path, "deleted"})
		}
	}
	sort.Slice(changes, func(i, j int) bool { return changes[i].Path < changes[j].Path })
	if len(changes) > changeLimit {
		changes = changes[:changeLimit]
	}
	return changes
}
