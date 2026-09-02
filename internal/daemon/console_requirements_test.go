package daemon

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/crmne/hyprmoncfg/internal/console"
	"github.com/crmne/hyprmoncfg/internal/ipc"
)

// seedRequirements puts an answer in the cache so the check does not shell out
// to systemctl or walk PATH, which is what makes this hermetic.
func seedRequirements(svc *Service, reqs []console.Requirement) {
	svc.consoleReqMu.Lock()
	defer svc.consoleReqMu.Unlock()
	svc.consoleReqs = reqs
	svc.consoleReqAt = time.Now()
}

// The panel used to read a hand-copied subset of the doctor's list that never
// mentioned gamescope, Steam or the systemd target. It could therefore call a
// machine ready that `console doctor` refused, and the button it drew led to a
// closed desktop. Both now read the same list.
func TestConsoleProblemsAreTheDoctorsOwnList(t *testing.T) {
	svc := &Service{}
	seedRequirements(svc, []console.Requirement{
		{OK: true, Have: "the gamescope session is installed"},
		{OK: false, Want: "gamescope is not installed"},
		{OK: false, Want: "Steam is not installed"},
	})

	problems := svc.consoleProblems(context.Background(), console.Config{}, ipc.ConsoleState{}, nil, "/tmp/console.json")
	if len(problems) != 2 {
		t.Fatalf("problems = %q, want the two unmet requirements", problems)
	}
	for _, want := range []string{"gamescope is not installed", "Steam is not installed"} {
		if !containsAny(problems, want) {
			t.Errorf("%q is missing from %q", want, problems)
		}
	}
}

// MissingSessionEnv was computed on every status and then dropped on the floor.
// It matters: the daemon is what launches Steam when a trigger fires, and
// without a graphical session environment those children exit immediately
// saying nothing the user will ever see.
func TestConsoleProblemsReportAMissingSessionEnvironment(t *testing.T) {
	svc := &Service{}
	seedRequirements(svc, nil)

	state := ipc.ConsoleState{MissingSessionEnv: []string{"WAYLAND_DISPLAY", "DISPLAY"}}
	problems := svc.consoleProblems(context.Background(), console.Config{}, state, nil, "/tmp/console.json")

	if !containsAny(problems, "WAYLAND_DISPLAY") || !containsAny(problems, "DISPLAY") {
		t.Fatalf("problems = %q, want the missing variables named", problems)
	}
	if !containsAny(problems, "restart hyprmoncfgd") {
		t.Errorf("problems = %q, want an instruction rather than only a complaint", problems)
	}
}

// A machine that meets everything reports nothing, which is what Ready is
// derived from. A list that always had something on it would make Ready
// permanently false and the panel permanently wrong.
func TestConsoleProblemsAreEmptyOnACompleteMachine(t *testing.T) {
	svc := &Service{}
	seedRequirements(svc, []console.Requirement{{OK: true, Have: "everything"}})

	if problems := svc.consoleProblems(context.Background(), console.Config{}, ipc.ConsoleState{}, nil, "/x"); len(problems) != 0 {
		t.Fatalf("problems = %q, want none", problems)
	}
}

// Status() broadcasts on every monitor event and to every connected panel, and
// the checks behind the list run systemctl and walk PATH. Re-running them per
// broadcast is the overhead this cache exists to avoid.
func TestConsoleRequirementsAreReusedWithinTheTTL(t *testing.T) {
	svc := &Service{}
	seedRequirements(svc, []console.Requirement{{OK: false, Want: "seeded"}})

	for i := 0; i < 3; i++ {
		reqs := svc.consoleRequirements(context.Background(), console.Config{}, nil, "/x")
		if len(reqs) != 1 || reqs[0].Want != "seeded" {
			t.Fatalf("call %d recomputed the list: %+v", i, reqs)
		}
	}
}

// A stale answer has to be refreshed, or `pacman -S gamescope` would never be
// noticed by a daemon that has been up for a week.
func TestConsoleRequirementsExpire(t *testing.T) {
	svc := &Service{}
	seedRequirements(svc, []console.Requirement{{OK: false, Want: "seeded"}})
	svc.consoleReqMu.Lock()
	svc.consoleReqAt = time.Now().Add(-consoleRequirementsTTL - time.Second)
	svc.consoleReqMu.Unlock()

	reqs := svc.consoleRequirements(context.Background(), console.Config{}, nil, "/x")
	if len(reqs) == 1 && reqs[0].Want == "seeded" {
		t.Fatal("an expired answer was reused")
	}
}

func containsAny(list []string, want string) bool {
	for _, s := range list {
		if strings.Contains(s, want) {
			return true
		}
	}
	return false
}
