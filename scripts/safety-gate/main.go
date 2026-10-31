package main

import (
	"fmt"
	"os"
)

func main() {
	root := "."
	if len(os.Args) > 1 {
		root = os.Args[1]
	}
	violations, err := Check(root)
	if err != nil {
		fmt.Fprintf(os.Stderr, "safety-gate: %v\n", err)
		os.Exit(2)
	}
	if len(violations) == 0 {
		return
	}
	fmt.Fprintln(os.Stderr, "safety-gate: destructive sinks found outside safe/trash/owner:")
	for _, v := range violations {
		fmt.Fprintf(os.Stderr, "  %s\n", v)
	}
	os.Exit(1)
}
