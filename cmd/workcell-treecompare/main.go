package main

import (
	"fmt"
	"os"

	"github.com/omkhar/workcell/internal/paritytree"
)

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintf(os.Stderr, "usage: %s LEFT_ROOT RIGHT_ROOT\n", os.Args[0])
		os.Exit(64)
	}
	if err := paritytree.CompareDirectoryTrees(os.Args[1], os.Args[2]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
