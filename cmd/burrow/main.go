// Command burrow is a Linux-native disk-space analyzer and prescriptive cleaner.
package main

import (
	"os"

	"github.com/brandtg/burrow/internal/cli"
)

func main() {
	os.Exit(cli.Execute())
}
