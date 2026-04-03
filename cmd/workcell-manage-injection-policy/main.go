package main

import (
	"os"

	"github.com/omkhar/workcell/internal/authpolicy"
)

func main() {
	os.Exit(authpolicy.Run(os.Args[0], os.Args[1:], os.Stdout, os.Stderr))
}
