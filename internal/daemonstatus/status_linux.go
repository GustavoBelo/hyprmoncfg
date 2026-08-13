package daemonstatus

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
)

const procRoot = "/proc"

// Running reports whether a hyprmoncfg daemon is present in the current
// process namespace.
func Running() bool {
	return runningIn(procRoot)
}

func runningIn(root string) bool {
	entries, err := os.ReadDir(root)
	if err != nil {
		return false
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		if _, err := strconv.Atoi(entry.Name()); err != nil {
			continue
		}

		cmdline, err := os.ReadFile(filepath.Join(root, entry.Name(), "cmdline"))
		if err == nil && isDaemonCommand(cmdline) {
			return true
		}
	}

	return false
}

func isDaemonCommand(cmdline []byte) bool {
	// Wrappers can change the kernel process name while preserving argv[0].
	argv0, _, _ := bytes.Cut(cmdline, []byte{0})
	return filepath.Base(string(argv0)) == "hyprmoncfgd"
}
