package main

import (
	"context"
	"wick_server/wickfs"
)

func cmdLs(args []string) {
	path := "."
	if len(args) > 0 && args[0] != "" {
		path = args[0]
	}

	fs := wickfs.NewLocalFS()
	entries, err := fs.Ls(context.Background(), path)
	if err != nil {
		writeError(err.Error())
		return
	}
	writeOK(entries)
}
