package couch

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/crmne/hyprmoncfg/internal/couch/hooks"
	"github.com/crmne/hyprmoncfg/internal/profile"
)

const (
	stateAppName    = "hyprmoncfg"
	sessionFileName = "session.json"
	logFileName     = "couch.log"
	historyDirName  = "history"
	logTailLines    = 50
	logMaxBytes     = 512 * 1024
	maxHistoryFiles = 50
)

// Phase is where a session is in its lifecycle. It is persisted so a process
// that dies mid-transition can be reconciled rather than merely reported stale.
type Phase string

const (
	PhaseIdle     Phase = "idle"
	PhaseEntering Phase = "entering"
	PhasePlaying  Phase = "playing"
	PhaseLeaving  Phase = "leaving"
)

// DeskSnapshotName labels the layout captured when a session starts.
const DeskSnapshotName = "couch-desk-snapshot"

type Session struct {
	PID       int       `json:"pid"`
	Phase     Phase     `json:"phase"`
	StartedAt time.Time `json:"started_at"`
	// Desk is the layout that was live when the session began.
	//
	// Storing the whole profile, rather than a profile name, is what lets a
	// killed session be undone: whoever finds the file next can put the exact
	// desktop back, including a layout that was never saved as a profile.
	Desk *profile.Profile `json:"desk,omitempty"`
	// Hooks is what each session hook found before it changed anything.
	//
	// It lives here rather than in a closure for the same reason Desk does: a
	// daemon killed mid-session loses every closure it held, and the desktop
	// would be left with the bar hidden and sound on a TV nobody is watching.
	Hooks map[string]hooks.State `json:"hooks,omitempty"`
}

// SnapshotDesk returns the layout a session recorded on the way in.
func SnapshotDesk(stateDir string) (profile.Profile, bool) {
	s, err := ReadSession(stateDir)
	if err != nil || s.Desk == nil || len(s.Desk.Outputs) == 0 {
		return profile.Profile{}, false
	}
	return *s.Desk, true
}

// OrphanedSession reports a recorded session whose process is gone and which
// still holds something to put back. The daemon reconciles these at
// startup: before, a SIGKILL left the TV layout applied and the desk dark, with
// nothing but a "stale" note in the status.
func OrphanedSession(stateDir string) (Session, bool) {
	s, stale := StaleSession(stateDir)
	if !stale {
		return Session{}, false
	}
	hasDesk := s.Desk != nil && len(s.Desk.Outputs) > 0
	if !hasDesk && len(s.Hooks) == 0 {
		return Session{}, false
	}
	return s, true
}

var ErrNoSession = errors.New("no couch mode session")

func StateDir() (string, error) {
	base := os.Getenv("XDG_STATE_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil || home == "" {
			return "", errors.New("unable to resolve state directory")
		}
		base = filepath.Join(home, ".local", "state")
	}
	return filepath.Join(base, stateAppName, "couch"), nil
}

func SessionPath(stateDir string) string {
	return filepath.Join(stateDir, sessionFileName)
}

func LogPath(stateDir string) string {
	return filepath.Join(stateDir, logFileName)
}

func WriteSession(stateDir string, s Session) error {
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(SessionPath(stateDir), append(data, '\n'), 0o644)
}

func ReadSession(stateDir string) (Session, error) {
	data, err := os.ReadFile(SessionPath(stateDir))
	if errors.Is(err, os.ErrNotExist) {
		return Session{}, ErrNoSession
	}
	if err != nil {
		return Session{}, err
	}
	var s Session
	if err := json.Unmarshal(data, &s); err != nil {
		return Session{}, fmt.Errorf("parse %s: %w", SessionPath(stateDir), err)
	}
	return s, nil
}

func ClearSession(stateDir string) {
	_ = os.Remove(SessionPath(stateDir))
}

func RunningSession(stateDir string) (Session, bool) {
	s, err := ReadSession(stateDir)
	if err != nil || s.PID <= 0 || !ProcessAlive(s.PID) {
		return Session{}, false
	}
	return s, true
}

// StaleSession reports a session file whose process has died. The caller
// should tell the user to run `hyprmoncfg couch restore` to recover.
func StaleSession(stateDir string) (Session, bool) {
	s, err := ReadSession(stateDir)
	if err != nil || s.PID <= 0 {
		return Session{}, false
	}
	if ProcessAlive(s.PID) {
		return Session{}, false
	}
	return s, true
}

func ProcessAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

func AppendLog(stateDir string, format string, args ...any) {
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		return
	}
	rotateLogIfNeeded(stateDir)
	f, err := os.OpenFile(LogPath(stateDir), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	message := fmt.Sprintf(format, args...)
	fmt.Fprintf(f, "%s %s\n", time.Now().Format(time.RFC3339), message)
}

func LogTail(stateDir string, maxLines int) []string {
	data, err := os.ReadFile(LogPath(stateDir))
	if err != nil {
		return nil
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) > maxLines {
		lines = lines[len(lines)-maxLines:]
	}
	return lines
}

func ownedByCurrentUser(pid int) bool {
	info, err := os.Stat(filepath.Join("/proc", strconv.Itoa(pid)))
	if err != nil {
		return false
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return true
	}
	return stat.Uid == uint32(os.Getuid())
}

func SteamPIDs(userOnly bool) []int {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil
	}
	pids := make([]int, 0, 4)
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil {
			continue
		}
		comm, err := os.ReadFile(filepath.Join("/proc", entry.Name(), "comm"))
		if err != nil {
			continue
		}
		if strings.TrimSpace(string(comm)) != "steam" {
			continue
		}
		if userOnly && !ownedByCurrentUser(pid) {
			continue
		}
		pids = append(pids, pid)
	}
	sort.Ints(pids)
	return pids
}

func HistoryDir(stateDir string) string {
	return filepath.Join(stateDir, historyDirName)
}

func rotateLogIfNeeded(stateDir string) {
	path := LogPath(stateDir)
	info, err := os.Stat(path)
	if err != nil || info.Size() < logMaxBytes {
		return
	}
	historyDir := HistoryDir(stateDir)
	if err := os.MkdirAll(historyDir, 0o755); err != nil {
		return
	}
	ts := time.Now().Format("20060102-150405")
	dest := filepath.Join(historyDir, "couch-"+ts+".log")
	if err := os.Rename(path, dest); err != nil {
		return
	}
	pruneHistory(historyDir)
}

func pruneHistory(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) <= maxHistoryFiles {
		return
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Name() < entries[j].Name()
	})
	for _, e := range entries[:len(entries)-maxHistoryFiles] {
		_ = os.Remove(filepath.Join(dir, e.Name()))
	}
}

func ListHistoryLogs(stateDir string) []string {
	historyDir := HistoryDir(stateDir)
	entries, err := os.ReadDir(historyDir)
	if err != nil {
		return nil
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	return names
}

func ReadHistoryLog(stateDir, name string) (string, error) {
	data, err := os.ReadFile(filepath.Join(HistoryDir(stateDir), name))
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func ClearLog(stateDir string) {
	path := LogPath(stateDir)
	if _, err := os.Stat(path); err != nil {
		return
	}
	historyDir := HistoryDir(stateDir)
	if err := os.MkdirAll(historyDir, 0o755); err != nil {
		return
	}
	ts := time.Now().Format("20060102-150405")
	dest := filepath.Join(historyDir, "couch-"+ts+".log")
	_ = os.Rename(path, dest)
	pruneHistory(historyDir)
}
