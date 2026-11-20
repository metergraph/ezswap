// Command ezswap replays production LLM calls on other models and
// reads what changes against a noise floor. See internal/cli.
package main

import (
	"os"

	"github.com/metergraph/ezswap/internal/cli"
)

func main() {
	os.Exit(cli.Run(os.Args[1:]))
}
