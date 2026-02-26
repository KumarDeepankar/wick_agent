package main

import (
	"fmt"
	"os"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintf(os.Stderr, "usage: wickfs <command> [args...]\n")
		fmt.Fprintf(os.Stderr, "commands: ls, read, write, edit, grep, glob, exec\n")
		os.Exit(2)
	}

	switch os.Args[1] {
	case "ls":
		cmdLs(os.Args[2:])
	case "read":
		cmdRead(os.Args[2:])
	case "write":
		cmdWrite(os.Args[2:])
	case "edit":
		cmdEdit(os.Args[2:])
	case "grep":
		cmdGrep(os.Args[2:])
	case "glob":
		cmdGlob(os.Args[2:])
	case "exec":
		cmdExec(os.Args[2:])
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n", os.Args[1])
		os.Exit(2)
	}
}
