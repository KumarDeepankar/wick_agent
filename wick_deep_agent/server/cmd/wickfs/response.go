package main

import (
	"encoding/json"
	"fmt"
	"os"
)

type response struct {
	OK    bool `json:"ok"`
	Data  any  `json:"data,omitempty"`
	Error string `json:"error,omitempty"`
}

func writeOK(data any) {
	out, _ := json.Marshal(response{OK: true, Data: data})
	fmt.Println(string(out))
}

func writeError(msg string) {
	out, _ := json.Marshal(response{OK: false, Error: msg})
	fmt.Println(string(out))
	os.Exit(1)
}
