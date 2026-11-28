package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime/debug"
	"sync"
	"time"

	"github.com/Sinkear/flowloop/internal/paths"
)

// The machine-side sidecar's operational log — connect lifecycle,
// dispatch receipt, run lifecycle, link failures. The daemon sees its own
// half of the link; the CLI's half was stdout-only and vanished with the
// terminal, leaving machine-side stalls invisible. One line per event,
// append-only, ~/.flowloop/cli.log (the authoritative copy), echoed to
// the terminal for the live view.
//
// SELF-CHECK (the log system must not fail silently): if ~/.flowloop cannot
// be created or cli.log cannot be opened, every line is written to STDERR
// instead and a one-shot marker goes there too — a sidecar that produces NO
// log at all was itself unattributable in the field (the log dir was
// unwritable and nothing said so). A failed write is also reported on stderr
// (rate-limited): the old code discarded it.
var (
	cliLogMu   sync.Mutex
	cliLogFile *os.File
	// cliLogFail is the reason the log file is unusable ("" = healthy). Held
	// under cliLogMu; read by the periodic health line and the exit report so
	// a degraded log is visible in the terminal AND in whatever sink exists.
	cliLogFail string
	// cliLogBrokenReported makes the stderr fallback notice one-shot — a
	// broken log dir must not become an unbounded stderr flood.
	cliLogBrokenReported bool
	// cliLogWriteErrs counts failed file writes since start (reported on the
	// next successful write, then reset).
	cliLogWriteErrs int
	// termEcho is the bounded terminal echo queue — a full queue DROPS the
	// line (the file has it). The echo goroutine is the ONLY thing allowed
	// to block on stdout: a stalled terminal must never block a logging
	// call site (a frozen sidecar is worse than a missed echo).
	termEcho = make(chan string, 64)
	// echoDrops counts lines dropped from the terminal echo (full queue); the
	// file still has them, so this is a display-only statistic.
	echoDrops int64
)

func init() {
	go func() {
		for line := range termEcho {
			fmt.Print(line)
		}
	}()
}

// stderrFallback writes one line to stderr when the log file is unavailable.
// Best-effort and never blocks a call site: stderr is normally the same pipe
// nohup redirected, but it is the ONLY remaining sink, so it must be tried.
func stderrFallback(line string) {
	fmt.Fprint(os.Stderr, line)
}

// cliLogOpen opens (or reopens) the log file. Caller holds cliLogMu.
func cliLogOpen() error {
	if _, err := paths.EnsureHome(); err != nil {
		return fmt.Errorf("home dir: %w", err)
	}
	f, err := os.OpenFile(paths.CLILog(), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("open %s: %w", filepath.Clean(paths.CLILog()), err)
	}
	cliLogFile = f
	cliLogFail = ""
	// Recovery re-arms the one-shot stderr notice: a SECOND breakage (the
	// reopen succeeded, then the disk filled again) must report its cause too,
	// otherwise only the bare lines keep flowing to stderr with no reason.
	cliLogBrokenReported = false
	return nil
}

// cliLogf logs one line to the log file (synchronous — the record of
// record) and best-effort echoes it to the terminal (never blocks).
func cliLogf(format string, args ...any) {
	line := fmt.Sprintf("%s %s\n", time.Now().Format("2006-01-02T15:04:05.000Z07:00"), fmt.Sprintf(format, args...))
	cliLogMu.Lock()
	defer cliLogMu.Unlock()
	if cliLogFile == nil {
		if err := cliLogOpen(); err != nil {
			cliLogFail = err.Error()
			// No file: stderr is the only sink left. Report the cause ONCE
			// (with the timestamp so the line still carries its time), then
			// keep emitting the real log lines there.
			if !cliLogBrokenReported {
				cliLogBrokenReported = true
				stderrFallback(fmt.Sprintf("%s cli.log UNUSABLE (%v) — logging to stderr only\n", time.Now().Format("2006-01-02T15:04:05.000Z07:00"), err))
			}
			stderrFallback(line)
			return
		}
	}
	if _, err := cliLogFile.WriteString(line); err != nil {
		// A failed write (disk full, fd closed underneath us) was silently
		// discarded before. Count it, tell the user once per failure run, and
		// drop the file handle so the next call reopens it.
		cliLogWriteErrs++
		if cliLogWriteErrs == 1 {
			stderrFallback(fmt.Sprintf("%s cli.log WRITE FAILED (%v) — log rotated/disk full, reopening\n", time.Now().Format("2006-01-02T15:04:05.000Z07:00"), err))
		}
		_ = cliLogFile.Close()
		cliLogFile = nil
		cliLogFail = fmt.Sprintf("write: %v", err)
		stderrFallback(line)
		return
	}
	if cliLogWriteErrs > 0 {
		// Recovered: say how many lines may be missing.
		cliLogWriteErrs = 0
	}
	select {
	case termEcho <- line:
	default: // queue full — the file still has it
		echoDrops++
	}
}

// cliLogAttachCrashOutput routes the runtime's FATAL crash trace into
// cli.log. The main goroutine's panics are covered by connectCmd's recover,
// but a panic in ANY other goroutine (every exec/chat/poll goroutine) kills
// the process with a stack printed only to stderr — which nohup discards, so
// the machine-side record was empty exactly when it mattered most (the
// incident's connect vanished without a line). SetCrashOutput tees the trace
// into the log file in ADDITION to stderr. It duplicates the descriptor, so
// the handle is closed immediately after.
func cliLogAttachCrashOutput() error {
	if _, err := paths.EnsureHome(); err != nil {
		return fmt.Errorf("home dir: %w", err)
	}
	f, err := os.OpenFile(paths.CLILog(), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("open %s: %w", filepath.Clean(paths.CLILog()), err)
	}
	defer f.Close() // SetCrashOutput dups the fd
	if err := debug.SetCrashOutput(f, debug.CrashOptions{}); err != nil {
		return fmt.Errorf("SetCrashOutput: %w", err)
	}
	return nil
}

// cliLogHealth reports the log subsystem's own state — a "no log lines" gap
// must be attributable to a broken sink rather than a silent process.
func cliLogHealth() string {
	cliLogMu.Lock()
	defer cliLogMu.Unlock()
	if cliLogFile == nil {
		if cliLogFail == "" {
			return "cli.log: not opened yet"
		}
		return "cli.log: UNUSABLE — " + cliLogFail
	}
	if cliLogWriteErrs > 0 {
		return fmt.Sprintf("cli.log: %d write error(s) since last success", cliLogWriteErrs)
	}
	path := paths.CLILog()
	if fi, err := os.Stat(path); err == nil {
		return fmt.Sprintf("cli.log: ok (%s, %d bytes)", path, fi.Size())
	}
	return "cli.log: ok"
}
