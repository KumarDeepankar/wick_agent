package main

import (
	"context"
	"wick_server/wickfs"
)

func cmdRead(args []string) {
	if len(args) < 1 {
		writeError("usage: wickfs read <path>")
		return
	}

	fs := wickfs.NewLocalFS()
	content, err := fs.ReadFile(context.Background(), args[0])
	if err != nil {
		writeError(err.Error())
		return
	}
	writeOK(content)
}
