package ipc

import (
	"fmt"
	"os"
	"path/filepath"
)

const SocketName = "hyprmoncfgd.sock"

func SocketPath() (string, error) {
	runtimeDir := os.Getenv("XDG_RUNTIME_DIR")
	if runtimeDir == "" {
		return "", fmt.Errorf("XDG_RUNTIME_DIR is not set")
	}
	if !filepath.IsAbs(runtimeDir) {
		return "", fmt.Errorf("XDG_RUNTIME_DIR must be absolute")
	}
	return filepath.Join(runtimeDir, SocketName), nil
}
