// Command apitypes writes the SPA's API types (src/lib/api.gen.ts) from the
// Go structs: apitypes <output file>.
package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/Automaat/ohara/backend-go/internal/apitypes"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "apitypes:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) != 1 {
		return errors.New("usage: apitypes <output file>")
	}
	var b bytes.Buffer
	if err := apitypes.Generate(&b, apitypes.Header, apitypes.Ohara()); err != nil {
		return err
	}
	root, err := os.OpenRoot(filepath.Dir(args[0]))
	if err != nil {
		return err
	}
	defer root.Close()
	return root.WriteFile(filepath.Base(args[0]), b.Bytes(), 0o600)
}
