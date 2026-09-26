package claude

import (
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type Session struct {
	Project  string `json:"project"`
	Path     string `json:"path"`
	Files    int    `json:"files"`
	SizeKB   int64  `json:"size_kb"`
	Modified int64  `json:"modified"`
}

type Overview struct {
	Home     string    `json:"home"`
	Sessions []Session `json:"sessions"`
	Memory   []string  `json:"memory"`
	Plans    []string  `json:"plans"`
}

func Collect(home string) (*Overview, error) {
	o := &Overview{Home: home}
	projects := filepath.Join(home, "projects")
	if entries, err := os.ReadDir(projects); err == nil {
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			p := filepath.Join(projects, e.Name())
			var files int
			var size int64
			var mod int64
			_ = filepath.WalkDir(p, func(path string, d fs.DirEntry, err error) error {
				if err != nil || d.IsDir() {
					return nil
				}
				files++
				info, err := d.Info()
				if err == nil {
					size += info.Size()
					if info.ModTime().Unix() > mod {
						mod = info.ModTime().Unix()
					}
				}
				return nil
			})
			o.Sessions = append(o.Sessions, Session{
				Project: decodeName(e.Name()), Path: p, Files: files, SizeKB: size / 1024, Modified: mod,
			})
		}
	}
	sort.Slice(o.Sessions, func(i, j int) bool { return o.Sessions[i].Modified > o.Sessions[j].Modified })

	mem := filepath.Join(home, "projects", "-", "memory")
	if entries, err := os.ReadDir(mem); err == nil {
		for _, e := range entries {
			if !e.IsDir() {
				o.Memory = append(o.Memory, e.Name())
			}
		}
	}
	plans := filepath.Join(home, "plans")
	if entries, err := os.ReadDir(plans); err == nil {
		for _, e := range entries {
			if !e.IsDir() {
				o.Plans = append(o.Plans, e.Name())
			}
		}
	}
	return o, nil
}

func decodeName(s string) string {
	return strings.ReplaceAll(s, "-", "/")
}
