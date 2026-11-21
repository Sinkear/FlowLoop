package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/Sinkear/ouroflow/internal/link"
)

// env-change — the scratch 环境变更 ledger (CLI 分支): a non-git domain's
// deliverable is an environment mutation (version swap, snapshot), so the
// agent records before/after versions + a rollback anchor here instead of a
// git commit. Goal/run/operator identity come from the run token.

func envChangeCmd(args []string) {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "usage: ouroflow env-change <record|list>")
		os.Exit(2)
	}
	switch args[0] {
	case "record":
		fs := flag.NewFlagSet("env-change record", flag.ExitOnError)
		envID := fs.String("env", "", "logical environment id (required)")
		status := fs.String("status", "", "started|succeeded|failed|rolled_back (required)")
		before := fs.String("before", "", "version before the mutation")
		after := fs.String("after", "", "version after the mutation")
		snapshot := fs.String("snapshot", "", "rollback anchor (id/path/url — no secrets)")
		manifest := fs.String("manifest-sha256", "", "sha256 of the deploy manifest (64 lowercase hex)")
		node := fs.String("node", "", "blueprint node key")
		startedAt := fs.String("started-at", "", "RFC3339Nano start time (default now)")
		finishedAt := fs.String("finished-at", "", "RFC3339Nano finish time (default now for terminal statuses)")
		errText := fs.String("error", "", "failure text (required for status=failed)")
		_ = fs.Parse(args[1:])
		if *envID == "" || *status == "" {
			fail("--env and --status are required")
		}
		var out map[string]any
		if err := rpcCall(link.MethodEnvChangeRecord, link.EnvChangeRecordParams{
			RPCToken:       rpcToken(),
			EnvID:          *envID,
			NodeKey:        *node,
			BeforeVersion:  *before,
			AfterVersion:   *after,
			SnapshotRef:    *snapshot,
			ManifestSHA256: *manifest,
			Status:         *status,
			StartedAt:      *startedAt,
			FinishedAt:     *finishedAt,
			Error:          *errText,
		}, &out); err != nil {
			fail("%v", err)
		}
		rpcPrintJSON(out)
	case "list":
		var out []map[string]any
		if err := rpcCall(link.MethodEnvChangeList, link.EnvChangeListParams{
			RPCToken: rpcToken(),
		}, &out); err != nil {
			fail("%v", err)
		}
		rpcPrintJSON(out)
	default:
		fmt.Fprintf(os.Stderr, "unknown env-change subcommand %q\n", args[0])
		os.Exit(2)
	}
}
