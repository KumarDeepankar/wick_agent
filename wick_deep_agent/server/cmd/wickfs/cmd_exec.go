package main

import (
	"os/exec"
	"strings"
)

type execResult struct {
	Stdout   string `json:"stdout"`
	Stderr   string `json:"stderr"`
	ExitCode int    `json:"exit_code"`
}

func cmdExec(args []string) {
	if len(args) < 1 {
		writeError("usage: wickfs exec <command>")
		return
	}

	command := strings.Join(args, " ")

	cmd := exec.Command("sh", "-c", command)
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()

	exitCode := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			writeError("failed to execute: " + err.Error())
			return
		}
	}

	writeOK(execResult{
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
		ExitCode: exitCode,
	})
}
