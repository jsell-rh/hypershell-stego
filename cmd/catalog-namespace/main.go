// Command catalog-namespace calculates a database namespace for an upgrade.
package main

import (
	"fmt"
	"os"

	"github.com/jsell-rh/hypershell-stego/internal/catalog"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "Usage: catalog-namespace <database-ksuid>")
		os.Exit(2)
	}
	namespace, err := catalog.DatabaseNamespace(os.Args[1])
	if err != nil {
		fmt.Fprintln(os.Stderr, "The database ID must be a canonical, nonzero KSUID.")
		os.Exit(2)
	}
	fmt.Println(namespace)
}
