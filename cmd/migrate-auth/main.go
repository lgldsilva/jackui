// Command migrate-auth is the one-shot ETL that imports the legacy SQLite
// auth.db into the PostgreSQL schema. It is a separate binary so the server
// (cmd/server) does not link a SQLite driver.
//
//	go run ./cmd/migrate-auth --from /data/auth.db --to postgres://…
package main

import (
	"fmt"
	"io"
	"os"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stderr))
}

// run executes the migration and maps the result to a process exit code.
func run(args []string, stderr io.Writer) int {
	if err := runMigrateAuth(args); err != nil {
		_, _ = fmt.Fprintf(stderr, "migrate-auth: %v\n", err)
		return 1
	}
	return 0
}
