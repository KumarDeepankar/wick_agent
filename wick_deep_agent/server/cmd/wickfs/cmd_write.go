package main

import (
	"context"
	"io"
	"os"
	"wick_server/wickfs"
)

func cmdWrite(args []string) {
	if len(args) < 1 {
		writeError("usage: wickfs write <path> (content on stdin)")
		return
	}

	content, err := io.ReadAll(os.Stdin)
	if err != nil {
		writeError("failed to read stdin: " + err.Error())
		return
	}

	fs := wickfs.NewLocalFS()
	result, err := fs.WriteFile(context.Background(), args[0], string(content))
	if err != nil {
		writeError(err.Error())
		return
	}
	writeOK(result)
}
