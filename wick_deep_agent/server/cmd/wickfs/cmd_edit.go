package main

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"wick_server/wickfs"
)

type editInput struct {
	OldText string `json:"old_text"`
	NewText string `json:"new_text"`
}

func cmdEdit(args []string) {
	if len(args) < 1 {
		writeError("usage: wickfs edit <path> (JSON {old_text, new_text} on stdin)")
		return
	}

	stdinData, err := io.ReadAll(os.Stdin)
	if err != nil {
		writeError("failed to read stdin: " + err.Error())
		return
	}

	var input editInput
	if err := json.Unmarshal(stdinData, &input); err != nil {
		writeError("invalid JSON input: " + err.Error())
		return
	}

	fs := wickfs.NewLocalFS()
	result, err := fs.EditFile(context.Background(), args[0], input.OldText, input.NewText)
	if err != nil {
		writeError(err.Error())
		return
	}
	writeOK(result)
}
