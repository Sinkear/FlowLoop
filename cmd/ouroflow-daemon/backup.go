package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/Sinkear/ouroflow/internal/backup"
	"github.com/Sinkear/ouroflow/internal/paths"
)

// backupCmd implements `ouroflow-daemon backup`: a consistent SQLite snapshot
// that is safe to run while the daemon is up, plus optional run files.
func backupCmd(args []string) {
	fs := flag.NewFlagSet("backup", flag.ExitOnError)
	out := fs.String("out", "", "output directory (required)")
	db := fs.String("db", "", "SQLite path (default $OUROFLOW_HOME/ouroflow.db)")
	withFiles := fs.Bool("with-files", false, "also archive runs/skills and agents.yaml")
	_ = fs.Parse(args)
	if *out == "" {
		fmt.Fprintln(os.Stderr, "usage: ouroflow-daemon backup -out DIR [-db PATH] [-with-files]")
		os.Exit(2)
	}
	if _, err := paths.EnsureHome(); err != nil {
		fmt.Fprintf(os.Stderr, "ouroflow home: %v\n", err)
		os.Exit(1)
	}
	res, err := backup.Create(context.Background(), backup.Options{
		DBPath:    *db,
		OutDir:    *out,
		WithFiles: *withFiles,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "backup: %v\n", err)
		os.Exit(1)
	}
	raw, _ := json.MarshalIndent(res, "", "  ")
	fmt.Println(string(raw))
}
