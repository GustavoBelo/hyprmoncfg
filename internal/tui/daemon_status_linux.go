package tui

import "github.com/crmne/hyprmoncfg/internal/daemonstatus"

func isDaemonRunning() bool {
	return daemonstatus.Running()
}
