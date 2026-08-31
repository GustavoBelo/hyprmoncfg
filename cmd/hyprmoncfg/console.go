package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/crmne/hyprmoncfg/internal/apps"
	"github.com/crmne/hyprmoncfg/internal/config"
	"github.com/crmne/hyprmoncfg/internal/console"
	"github.com/crmne/hyprmoncfg/internal/hypr"
)

func newConsoleCmd(configDir *string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "console",
		Short: "Console Mode: hand the machine to Steam's gamescope session and back",
		Long: "Console Mode replaces the desktop with Steam's gamescope session, on the\n" +
			"TV, and brings the desktop back when you leave Big Picture. Entering\n" +
			"closes your desktop session, so anything unsaved is closed first.\n\n" +
			"It needs gamescope, Steam, and a gamescope session package installed,\n" +
			"and it needs `console setup` to have been done once. `console doctor`\n" +
			"says what is missing.",
		SilenceUsage: true,
	}
	cmd.AddCommand(
		newConsoleSessionCmd(configDir),
		newConsoleEnterCmd(configDir),
		newConsoleStatusCmd(configDir),
		newConsoleDoctorCmd(configDir),
		newConsoleSetupCmd(configDir),
		newConsoleTVCmd(configDir),
		newConsoleAppsCmd(configDir),
		newConsoleLeaveCmd(),
		newConsoleCancelCmd(),
		newConsoleTriggerCmd(configDir),
	)
	silenceUsageTree(cmd)
	return cmd
}

// silenceUsageTree pushes the flag down the tree: cobra consults the root or the
// executed command, never an intermediate parent, and these commands fail for
// reasons the user has to act on -- no gamescope session, no TV -- where
// repeating the flag list buries the sentence that matters.
func silenceUsageTree(cmd *cobra.Command) {
	cmd.SilenceUsage = true
	for _, child := range cmd.Commands() {
		silenceUsageTree(child)
	}
}

func onOff(b bool) string {
	if b {
		return "on"
	}
	return "off"
}

// newConsoleSessionCmd is what the login manager runs. Everything else in this
// file exists to talk to it.
func newConsoleSessionCmd(configDir *string) *cobra.Command {
	return &cobra.Command{
		Use:   "session",
		Short: "Host the desktop and the console in one login session (run by the login manager)",
		Long: "This is the command a session entry points at. It runs your desktop\n" +
			"compositor, and when something asks to switch it runs the gamescope\n" +
			"session instead, then your desktop again. Running it by hand from\n" +
			"inside a desktop will not work: it has to be what the login manager\n" +
			"starts.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			base, err := config.EnsureBaseDir(*configDir)
			if err != nil {
				return err
			}
			cfg, err := console.LoadConfig(base)
			if err != nil {
				return err
			}
			runtimeDir, err := console.RuntimeDir()
			if err != nil {
				return err
			}
			stateDir, err := consoleStateDir()
			if err != nil {
				return err
			}

			entries := console.FindEntries(console.SessionDirs())
			desktop, ok := console.FindEntryByFile(entries, cfg.DesktopSession)
			if !ok {
				return fmt.Errorf("the desktop session %q was not found; set desktop_session in %s",
					cfg.DesktopSession, console.ConfigPath(base))
			}
			if console.HostsConsole(desktop) {
				return fmt.Errorf("the configured desktop session %s is a hosting session; it would host itself forever", desktop.File())
			}
			gamescope, hasGamescope := console.FindGamescopeSession(entries)

			// A session's stderr goes wherever the login manager decided, which
			// on SDDM is nowhere reachable. Without a file there is no way to
			// find out why a session that lasted five seconds gave up.
			logf := consoleLogger(stateDir)

			w := &console.Wrapper{
				DesktopExec:   desktop.Exec,
				StateDir:      stateDir,
				RuntimeDir:    runtimeDir,
				TVDescription: cfg.TVDescription,
				TVConnector:   cfg.TVName,
				Logf:          logf,
			}
			if hasGamescope {
				w.ConsoleExec = gamescope.Exec
				w.ConsoleSessionName = strings.TrimSuffix(gamescope.File(), ".desktop")
			}
			return w.Run(ctx)
		},
	}
}

func newConsoleEnterCmd(configDir *string) *cobra.Command {
	var yes bool
	var wait time.Duration
	cmd := &cobra.Command{
		Use:   "enter",
		Short: "Close the desktop and start the console session",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			base, err := config.EnsureBaseDir(*configDir)
			if err != nil {
				return err
			}
			cfg, err := console.LoadConfig(base)
			if err != nil {
				return err
			}
			runtimeDir, err := console.RuntimeDir()
			if err != nil {
				return err
			}
			if !console.Hosted(runtimeDir) {
				return fmt.Errorf("this session is not hosted by `hyprmoncfg console session`, so there would be no way back.\nRun `hyprmoncfg console setup` and log in again")
			}
			if !cfg.Configured() {
				return fmt.Errorf("no TV has been chosen; run `hyprmoncfg console doctor`")
			}

			if !yes {
				fmt.Printf("Entering console mode closes your desktop session.\n")
				fmt.Printf("Everything open will be closed. Continuing in %s -- Ctrl-C to stop.\n", wait)
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-time.After(wait):
				}
			}

			// Entering takes the desktop down with everything on it, so
			// anything the user tracked gets asked to close first -- while
			// there is still a compositor for it to put a save dialog on.
			if len(cfg.AppsToClose) > 0 {
				client, err := hypr.NewClient()
				if err != nil {
					return err
				}
				result := apps.CloseTrackedApps(ctx, client, cfg.AppsToClose)
				fmt.Println(apps.DescribeCloseResult(result, cfg.AppsToClose))
			}

			if err := console.Request(runtimeDir, console.ModeConsole); err != nil {
				return err
			}
			if err := console.StopCompositor(ctx, runtimeDir); err != nil {
				// The request would otherwise fire at the next logout, which is
				// not what anyone asked for.
				console.ClearRequest(runtimeDir)
				return err
			}
			return nil
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "do not wait before closing the desktop")
	cmd.Flags().DurationVar(&wait, "wait", 5*time.Second, "how long to wait before closing the desktop")
	return cmd
}

func newConsoleStatusCmd(configDir *string) *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show what console mode is configured to do",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			base, err := config.EnsureBaseDir(*configDir)
			if err != nil {
				return err
			}
			cfg, err := console.LoadConfig(base)
			if err != nil {
				return err
			}
			fmt.Printf("TV display:      %s\n", orNone(cfg.TVName))
			fmt.Printf("Desktop session: %s\n", orNone(cfg.DesktopSession))
			fmt.Printf("Apps to close:   %s\n", orNone(strings.Join(cfg.AppsToClose, ", ")))
			if runtimeDir, err := console.RuntimeDir(); err == nil {
				fmt.Printf("Hosted session:  %s\n", yesNo(console.Hosted(runtimeDir)))
			}
			return nil
		},
	}
}

func newConsoleDoctorCmd(configDir *string) *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Check that a console session would work",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			base, err := config.EnsureBaseDir(*configDir)
			if err != nil {
				return err
			}
			cfg, _ := console.LoadConfig(base)
			sc := console.Systemctl{}
			ready := true

			check := func(ok bool, good, bad string) {
				if ok {
					fmt.Printf("ok    %s\n", good)
					return
				}
				fmt.Printf("PROBLEM %s\n", bad)
				ready = false
			}

			entries := console.FindEntries(console.SessionDirs())
			gamescope, hasGamescope := console.FindGamescopeSession(entries)
			check(hasGamescope,
				fmt.Sprintf("the gamescope session is installed (%s)", gamescope.File()),
				"no gamescope session is installed; install a gamescope-session package")
			check(console.TargetKnown(ctx, sc),
				"systemd can resolve gamescope-session.target",
				"systemd cannot resolve gamescope-session.target")
			check(have("gamescope"), "gamescope is installed", "gamescope is not installed")
			check(have("steam"), "Steam is installed", "Steam is not installed")

			_, hasDesktop := console.FindEntryByFile(entries, cfg.DesktopSession)
			check(hasDesktop,
				fmt.Sprintf("the desktop session to come back to is %s", cfg.DesktopSession),
				"no desktop session to come back to; set desktop_session in "+console.ConfigPath(base))

			check(cfg.Configured(),
				fmt.Sprintf("the TV is %s", cfg.TVName),
				"no TV has been chosen")

			if runtimeDir, err := console.RuntimeDir(); err == nil {
				check(console.Hosted(runtimeDir),
					"this session is hosted, so switching will work",
					"this session is not hosted; run `hyprmoncfg console setup` and log in again")
			}

			if dirty, why := console.Dirty(ctx, sc); dirty {
				fmt.Printf("warn  %s\n", why)
			}
			if cfg.TVDescription == "" {
				fmt.Printf("warn  the TV has no description recorded, so sound will stay where it is\n")
			}

			if !ready {
				return fmt.Errorf("console mode is not ready")
			}
			fmt.Println("\nReady for a console session.")
			return nil
		},
	}
}

func newConsoleSetupCmd(configDir *string) *cobra.Command {
	return &cobra.Command{
		Use:   "setup",
		Short: "Say how to point the login manager at the hosting session",
		Long: "Prints what to change, and changes nothing. Getting this wrong leaves a\n" +
			"machine that will not present a desktop, and only one login manager has\n" +
			"been tested, so the decision stays with you.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			base, err := config.EnsureBaseDir(*configDir)
			if err != nil {
				return err
			}
			cfg, _ := console.LoadConfig(base)
			lm := console.DetectLoginManager(ctx, console.Systemctl{})

			entries := console.FindEntries(console.SessionDirs())
			// The session the user is in right now is the one they want back,
			// so asking them to name it would be asking a question we can
			// answer.
			if cfg.DesktopSession == "" {
				current := console.CurrentDesktopSession(ctx, console.Systemctl{})
				// Inside a hosted session the running session *is* the hosting
				// entry; recording that would make the wrapper host itself.
				if entry, found := console.FindEntryByFile(entries, current); found && console.HostsConsole(entry) {
					current = ""
				}
				if current != "" {
					cfg.DesktopSession = current
					if err := console.SaveConfig(base, cfg); err != nil {
						return err
					}
					fmt.Printf("Recorded %s as the desktop to come back to.\n\n", cfg.DesktopSession)
				}
			}
			desktop, ok := console.FindEntryByFile(entries, cfg.DesktopSession)
			if ok && console.HostsConsole(desktop) {
				return fmt.Errorf("desktop_session in %s points at the hosting session itself, which would host itself forever.\nSet it to the desktop you want back, one of: %s",
					console.ConfigPath(base), strings.Join(plainEntryFiles(entries), ", "))
			}
			if !ok {
				return fmt.Errorf("set desktop_session in %s first: it is the session you want to come back to.\nInstalled sessions: %s",
					console.ConfigPath(base), strings.Join(entryFiles(entries), ", "))
			}

			self, err := os.Executable()
			if err != nil {
				self = "hyprmoncfg"
			}
			wrapper := self + " console session"
			name := console.HostingEntryName(desktop.Name)

			staged := filepath.Join(base, "hyprmoncfg-session.desktop")
			body := console.EntryContent(name, wrapper, desktop.DesktopNames)
			if err := os.WriteFile(staged, []byte(body), 0o644); err != nil {
				return err
			}

			fmt.Printf("Login manager: %s\n\n", lm.Kind)
			fmt.Printf("Wrote the session entry to %s\n\n", staged)
			fmt.Print(console.SetupInstructions(lm, staged, name, wrapper))
			return nil
		},
	}
}

func have(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

// newConsoleTVCmd chooses which display the console plays on, recording all
// three things that identify it at once: the connector gamescope drives, the
// EDID key that survives a replug, and the description its audio is found by.
func newConsoleTVCmd(configDir *string) *cobra.Command {
	return &cobra.Command{
		Use:   "tv [connector]",
		Short: "Choose the display the console plays on, or list the candidates",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			client, err := hypr.NewClient()
			if err != nil {
				return err
			}
			monitors, err := client.Monitors(ctx)
			if err != nil {
				return err
			}
			if len(args) == 0 {
				base, err := config.EnsureBaseDir(*configDir)
				if err != nil {
					return err
				}
				cfg, _ := console.LoadConfig(base)
				for _, m := range monitors {
					mark := "  "
					if m.Name == cfg.TVName {
						mark = "* "
					}
					fmt.Printf("%s%-12s %s\n", mark, m.Name, m.Description)
				}
				return nil
			}

			for _, m := range monitors {
				if m.Name != args[0] {
					continue
				}
				base, err := config.EnsureBaseDir(*configDir)
				if err != nil {
					return err
				}
				cfg, err := console.LoadConfig(base)
				if err != nil {
					return err
				}
				cfg.TVName = m.Name
				cfg.TVKey = m.HardwareKey()
				cfg.TVDescription = m.Description
				if err := console.SaveConfig(base, cfg); err != nil {
					return err
				}
				fmt.Printf("The console will play on %s (%s).\n", m.Name, m.Description)
				return nil
			}
			return fmt.Errorf("no connected display is called %q", args[0])
		},
	}
}

// newConsoleLeaveCmd ends a console session from outside it.
//
// Big Picture's own "Switch to Desktop" is the normal way home. This exists for
// when that is not reachable -- over ssh, or when the TV is off and the console
// is playing to a display nobody can see.
func newConsoleLeaveCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "leave",
		Short: "End the console session and bring the desktop back",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			runtimeDir, err := console.RuntimeDir()
			if err != nil {
				return err
			}
			// The request is belt and braces: the wrapper treats a console that
			// exits with nothing pending as "go home" anyway, but saying so
			// leaves no room for a race with something else asking first.
			if err := console.Request(runtimeDir, console.ModeDesktop); err != nil {
				return err
			}
			if err := console.StopConsoleSession(ctx, console.Systemctl{}); err != nil {
				console.ClearRequest(runtimeDir)
				return err
			}
			fmt.Println("The console session is ending; the desktop is coming back.")
			return nil
		},
	}
}

// newConsoleCancelCmd calls off an automatic entry that has been announced but
// not happened yet.
func newConsoleCancelCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "cancel",
		Short: "Call off an automatic entry that is counting down",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			runtimeDir, err := console.RuntimeDir()
			if err != nil {
				return err
			}
			if err := console.RequestCancel(runtimeDir); err != nil {
				return err
			}
			fmt.Println("The desktop will stay. Switch the controller off and on to arm it again.")
			return nil
		},
	}
}

// newConsoleTriggerCmd turns the controller trigger on and off.
//
// It is off by default and says what it costs when turned on, because entering
// no longer rearranges displays -- it ends the desktop session.
func newConsoleTriggerCmd(configDir *string) *cobra.Command {
	return &cobra.Command{
		Use:       "trigger [on|off]",
		Short:     "Enter console mode when a controller is switched on",
		Args:      cobra.MaximumNArgs(1),
		ValidArgs: []string{"on", "off"},
		RunE: func(cmd *cobra.Command, args []string) error {
			base, err := config.EnsureBaseDir(*configDir)
			if err != nil {
				return err
			}
			cfg, err := console.LoadConfig(base)
			if err != nil {
				return err
			}
			if len(args) == 0 {
				fmt.Printf("Enter on controller connect: %s\n", onOff(cfg.EnterOnControllerConnect))
				return nil
			}
			cfg.EnterOnControllerConnect = args[0] == "on"
			if err := console.SaveConfig(base, cfg); err != nil {
				return err
			}
			if cfg.EnterOnControllerConnect {
				fmt.Println("Switching a controller on will now close your desktop session.")
				fmt.Println("You get 20 seconds and a notification; `hyprmoncfg console cancel` stops it.")
			} else {
				fmt.Println("Controllers no longer start a console session.")
			}
			return nil
		},
	}
}

// newConsoleAppsCmd manages what gets closed before the desktop goes away.
//
// Matching is exact -- a window class or a /proc comm, never a title substring --
// which makes the right value hard to guess by hand, so the list is picked from
// what is actually running rather than typed.
func newConsoleAppsCmd(configDir *string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "apps",
		Short: "Choose what to close before the desktop goes away",
	}

	cmd.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "Show what is tracked, and what could be",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			base, err := config.EnsureBaseDir(*configDir)
			if err != nil {
				return err
			}
			cfg, err := console.LoadConfig(base)
			if err != nil {
				return err
			}
			client, err := hypr.NewClient()
			if err != nil {
				return err
			}
			candidates := apps.CloseCandidates(ctx, client)
			chosen := apps.MarkChosen(candidates, cfg.AppsToClose)
			for _, c := range candidates {
				mark := "  "
				if chosen[c.Token] {
					mark = "* "
				}
				fmt.Printf("%s%-40s %s\n", mark, c.Token, c.Label)
			}
			if missing := apps.MissingTokens(candidates, cfg.AppsToClose); len(missing) > 0 {
				fmt.Printf("\ntracked but not running: %s\n", strings.Join(missing, ", "))
			}
			return nil
		},
	})

	cmd.AddCommand(&cobra.Command{
		Use:   "add <token>...",
		Short: "Track an application",
		Args:  cobra.MinimumNArgs(1),
		RunE:  func(cmd *cobra.Command, args []string) error { return editApps(*configDir, args, true) },
	})
	cmd.AddCommand(&cobra.Command{
		Use:   "remove <token>...",
		Short: "Stop tracking an application",
		Args:  cobra.MinimumNArgs(1),
		RunE:  func(cmd *cobra.Command, args []string) error { return editApps(*configDir, args, false) },
	})
	return cmd
}

func editApps(configDir string, tokens []string, add bool) error {
	base, err := config.EnsureBaseDir(configDir)
	if err != nil {
		return err
	}
	cfg, err := console.LoadConfig(base)
	if err != nil {
		return err
	}
	keep := map[string]bool{}
	for _, a := range cfg.AppsToClose {
		keep[a] = true
	}
	for _, t := range tokens {
		keep[t] = add
	}
	cfg.AppsToClose = nil
	for token, on := range keep {
		if on {
			cfg.AppsToClose = append(cfg.AppsToClose, token)
		}
	}
	cfg.AppsToClose = apps.SanitizeApps(cfg.AppsToClose)
	if err := console.SaveConfig(base, cfg); err != nil {
		return err
	}
	fmt.Printf("Now closing before the console starts: %s\n", orNone(strings.Join(cfg.AppsToClose, ", ")))
	return nil
}

// consoleLogger writes the hosting session's log where it can be read after the
// fact, and to stderr for whoever is watching.
func consoleLogger(stateDir string) func(string, ...any) {
	path := filepath.Join(stateDir, "console.log")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return func(format string, args ...any) { fmt.Fprintf(os.Stderr, format+"\n", args...) }
	}
	return func(format string, args ...any) {
		line := fmt.Sprintf(time.Now().Format(time.RFC3339)+" "+format+"\n", args...)
		fmt.Fprint(os.Stderr, line)
		_, _ = file.WriteString(line)
	}
}

func consoleStateDir() (string, error) {
	dir, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	dir = filepath.Join(dir, "hyprmoncfg")
	return dir, os.MkdirAll(dir, 0o755)
}

// plainEntryFiles lists the sessions that are candidates to come back to, which
// is every one that is not itself a hosting session.
func plainEntryFiles(entries []console.Entry) []string {
	files := []string{}
	for _, e := range entries {
		if !console.HostsConsole(e) {
			files = append(files, e.File())
		}
	}
	return files
}

func entryFiles(entries []console.Entry) []string {
	files := make([]string, 0, len(entries))
	for _, e := range entries {
		files = append(files, e.File())
	}
	return files
}

func orNone(s string) string {
	if strings.TrimSpace(s) == "" {
		return "not set"
	}
	return s
}

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}
