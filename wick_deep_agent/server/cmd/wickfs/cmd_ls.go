package main

import "os"

type lsEntry struct {
	Name string `json:"name"`
	Type string `json:"type"`
	Size int64  `json:"size"`
}

func cmdLs(args []string) {
	path := "."
	if len(args) > 0 && args[0] != "" {
		path = args[0]
	}

	entries, err := os.ReadDir(path)
	if err != nil {
		writeError(err.Error())
		return
	}

	result := make([]lsEntry, 0, len(entries))
	for _, e := range entries {
		info, err := e.Info()
		if err != nil {
			continue
		}
		typ := "file"
		if e.IsDir() {
			typ = "dir"
		} else if info.Mode()&os.ModeSymlink != 0 {
			typ = "symlink"
		}
		result = append(result, lsEntry{
			Name: e.Name(),
			Type: typ,
			Size: info.Size(),
		})
	}
	writeOK(result)
}
