package main

import (
	"context"
	"wick_server/wickfs"
)

func cmdGlob(args []string) {
	if len(args) < 1 {
		writeError("usage: wickfs glob <pattern> [path]")
		return
	}

	pattern := args[0]
	searchPath := "."
	if len(args) > 1 && args[1] != "" {
		searchPath = args[1]
	}

	fs := wickfs.NewLocalFS()
	result, err := fs.Glob(context.Background(), pattern, searchPath)
	if err != nil {
		writeError(err.Error())
		return
	}
	writeOK(result)
}
