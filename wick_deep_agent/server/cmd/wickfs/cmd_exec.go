package main

import (
	"context"
	"strings"
	"wick_server/wickfs"
)

func cmdExec(args []string) {
	if len(args) < 1 {
		writeError("usage: wickfs exec <command>")
		return
	}

	command := strings.Join(args, " ")

	fs := wickfs.NewLocalFS()
	result, err := fs.Exec(context.Background(), command)
	if err != nil {
		writeError(err.Error())
		return
	}
	writeOK(result)
}
