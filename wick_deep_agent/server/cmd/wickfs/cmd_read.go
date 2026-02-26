package main

import (
	"encoding/base64"
	"os"
	"unicode/utf8"
)

func cmdRead(args []string) {
	if len(args) < 1 {
		writeError("usage: wickfs read <path>")
		return
	}

	data, err := os.ReadFile(args[0])
	if err != nil {
		writeError(err.Error())
		return
	}

	// If valid UTF-8, return as string; otherwise base64-encode
	if utf8.Valid(data) {
		writeOK(string(data))
	} else {
		writeOK("base64:" + base64.StdEncoding.EncodeToString(data))
	}
}
