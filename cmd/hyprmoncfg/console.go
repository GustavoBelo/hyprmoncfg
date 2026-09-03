package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/crmne/hyprmoncfg/internal/config"
	"github.com/crmne/hyprmoncfg/internal/console"
	"github.com/crmne/hyprmoncfg/internal/hypr"
	"github.com/crmne/hyprmoncfg/internal/ipc"
	"github.com/crmne/hyprmoncfg/internal/notify"
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
		newConsoleLeaveCmd(),
		newConsoleCancelCmd(),
		newConsoleTriggerCmd(configDir),
		newConsoleBootCmd(configDir),
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
			stateDir, err := console.StateDir()
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
				DesktopExec:    desktop.Exec,
				DesktopSession: desktop.File(),
				StateDir:       stateDir,
				RuntimeDir:     runtimeDir,
				Choices:        cfg,
				Boot:           cfg.Boot,
				Logf:           logf,
				// This process is started once, by the login manager, and then
				// hosts every session until the user logs out. Everything it
				// was told above was true at login; the settings it acts on
				// have to be the ones in the file now.
				Reload: func() (console.Config, error) { return console.LoadConfig(base) },
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
			out := cmd.OutOrStdout()
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
			// Everything the session needs, checked before anything is closed.
			// This used to gate on the hosting marker and a chosen display only,
			// so a machine with no gamescope announced a countdown, ended the
			// desktop with everything open on it, failed inside the wrapper, and
			// came back to an empty desktop with nothing said anywhere the user
			// would look.
			entries := console.FindEntries(console.SessionDirs())
			if unmet := console.Unmet(console.Requirements(ctx, cfg, console.Systemctl{}, entries, console.ConfigPath(base))); len(unmet) > 0 {
				return fmt.Errorf("console mode is not ready, so the desktop stays:\n  - %s\nRun `hyprmoncfg console doctor` for the whole list", strings.Join(unmet, "\n  - "))
			}

			// --yes is the one path that never announces anything, and it has
			// to stay that way: it is what the daemon runs once its own
			// countdown has finished, and routing it back would be a loop.
			if yes {
				return enterNow(ctx, runtimeDir)
			}

			// The countdown belongs in the daemon, which outlives this process
			// -- and this process is about to be closed along with the rest of
			// the desktop.
			if enterViaDaemon(ctx, wait) {
				fmt.Fprintf(out, "Entering console mode closes your desktop session.\n")
				fmt.Fprintf(out, "Click the notification to cancel; `hyprmoncfg console cancel` stops it too.\n")
				return nil
			}

			// No daemon to hand it to, so count down here. The announcement is
			// the same, button and all: this process lives long enough to own
			// it, and the launcher entry has no terminal to print to.
			if wait <= 0 {
				wait = console.DefaultGrace
			}
			notifier := notify.Dial()
			defer notifier.Close()

			fmt.Fprintf(out, "Entering console mode closes your desktop session.\n")
			fmt.Fprintf(out, "Everything open will be closed. Continuing in %s -- Ctrl-C to stop.\n", wait)
			if err := console.Countdown(ctx, console.CountdownOpts{
				Grace:      wait,
				Display:    cfg.TVName,
				RuntimeDir: runtimeDir,
				Notifier:   notifier,
				Logf:       func(format string, args ...any) { fmt.Fprintf(os.Stderr, format+"\n", args...) },
			}); err != nil {
				return err
			}
			return enterNow(ctx, runtimeDir)
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "do not wait before closing the desktop")
	cmd.Flags().DurationVar(&wait, "wait", 0, "how long to wait before closing the desktop (0 uses the standard countdown)")
	return cmd
}

// enterNow is the point of no return. The request outlives this process on
// purpose -- the wrapper reads it only once the compositor has gone -- and
// stopping the compositor takes the desktop with it.
func enterNow(ctx context.Context, runtimeDir string) error {
	if err := console.Request(runtimeDir, console.ModeConsole); err != nil {
		return err
	}
	if err := console.StopCompositor(ctx, runtimeDir); err != nil {
		// The request would otherwise fire at the next logout, which is not
		// what anyone asked for.
		console.ClearRequest(runtimeDir)
		return err
	}
	return nil
}

// enterViaDaemon reports whether a running daemon took the request. A grace of
// zero leaves the length of the countdown to the daemon.
func enterViaDaemon(ctx context.Context, grace time.Duration) bool {
	path, err := ipc.SocketPath()
	if err != nil {
		return false
	}
	dialCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	client, err := ipc.Dial(dialCtx, path)
	if err != nil {
		return false
	}
	defer client.Close()
	return client.ConsoleEnter(dialCtx, "the command line", grace) == nil
}

func newConsoleStatusCmd(configDir *string) *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show what console mode is configured to do",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			out := cmd.OutOrStdout()
			base, err := config.EnsureBaseDir(*configDir)
			if err != nil {
				return err
			}
			cfg, err := console.LoadConfig(base)
			if err != nil {
				return err
			}
			fmt.Fprintf(out, "Starts in:       %s\n", cfg.Boot)
			fmt.Fprintf(out, "TV display:      %s\n", orNone(cfg.TVName))
			fmt.Fprintf(out, "Desktop session: %s\n", orNone(cfg.DesktopSession))
			if runtimeDir, err := console.RuntimeDir(); err == nil {
				fmt.Fprintf(out, "Hosted session:  %s\n", yesNo(console.Hosted(runtimeDir)))
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
			out := cmd.OutOrStdout()
			ctx := cmd.Context()
			base, err := config.EnsureBaseDir(*configDir)
			if err != nil {
				return err
			}
			cfg, _ := console.LoadConfig(base)
			sc := console.Systemctl{}
			ready := true

			// The same list the panel shows and every entry path refuses with.
			// Keeping it in one place is what stops the doctor from passing a
			// machine the daemon would refuse, or the other way round.
			entries := console.FindEntries(console.SessionDirs())
			for _, req := range console.Requirements(ctx, cfg, sc, entries, console.ConfigPath(base)) {
				if req.OK {
					fmt.Fprintf(out, "ok    %s\n", req.Have)
					continue
				}
				fmt.Fprintf(out, "PROBLEM %s\n", req.Want)
				ready = false
			}

			if dirty, why := console.Dirty(ctx, sc); dirty {
				fmt.Fprintf(out, "warn  %s\n", why)
			}
			if cfg.TVDescription == "" {
				fmt.Fprintf(out, "warn  the TV has no description recorded, so sound will stay where it is\n")
			}
			if note := bootLoginNote(ctx, cfg.Boot); note != "" {
				fmt.Fprintf(out, "warn  %s\n", note)
			}

			if !ready {
				return fmt.Errorf("console mode is not ready")
			}
			fmt.Fprintln(out, "\nReady for a console session.")
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
			out := cmd.OutOrStdout()
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
					fmt.Fprintf(out, "Recorded %s as the desktop to come back to.\n\n", cfg.DesktopSession)
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

			fmt.Fprintf(out, "Login manager: %s\n\n", lm.Kind)
			fmt.Fprintf(out, "Wrote the session entry to %s\n\n", staged)
			fmt.Fprint(out, console.SetupInstructions(lm, staged, name, wrapper))
			return nil
		},
	}
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
			out := cmd.OutOrStdout()
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
					fmt.Fprintf(out, "%s%-12s %s\n", mark, m.Name, m.Description)
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
				cfg.TVDescription = m.Description
				if err := console.SaveConfig(base, cfg); err != nil {
					return err
				}
				fmt.Fprintf(out, "The console will play on %s (%s).\n", m.Name, m.Description)
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
			out := cmd.OutOrStdout()
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
			fmt.Fprintln(out, "The console session is ending; the desktop is coming back.")
			return nil
		},
	}
}

// newConsoleCancelCmd calls off an entry that has been announced but not
// happened yet.
//
// The notification's own Cancel button is the usual way. This is the one that
// works over ssh, from a script, and on a desktop whose notification server
// cannot take an answer back.
func newConsoleCancelCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "cancel",
		Short: "Call off an entry that is counting down",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			out := cmd.OutOrStdout()
			ctx := cmd.Context()
			runtimeDir, err := console.RuntimeDir()
			if err != nil {
				return err
			}
			// Saying "the desktop will stay" when nothing was counting down is
			// worse than useless: it reads as having stopped something, and the
			// file left behind used to call off the *next* entry, silently, up
			// to half a minute later. Ask the daemon what is actually pending.
			if armed, known := entryIsArmed(ctx); known && !armed {
				fmt.Fprintln(out, "Nothing is counting down, so there was nothing to call off.")
				return nil
			}
			if err := console.RequestCancel(runtimeDir); err != nil {
				return err
			}
			fmt.Fprintln(out, "The desktop will stay.")
			return nil
		},
	}
}

// entryIsArmed asks the daemon whether an entry is counting down right now.
//
// The second return says whether the answer is worth anything: with no daemon
// there is nobody to ask, and the honest move is to leave the cancel behind for
// a countdown running in some other process rather than claim there is none.
func entryIsArmed(ctx context.Context) (armed, known bool) {
	path, err := ipc.SocketPath()
	if err != nil {
		return false, false
	}
	dialCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	client, err := ipc.Dial(dialCtx, path)
	if err != nil {
		return false, false
	}
	defer client.Close()
	state, err := client.ConsoleStatus(dialCtx)
	if err != nil {
		return false, false
	}
	return state.Arming, true
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
			out := cmd.OutOrStdout()
			base, err := config.EnsureBaseDir(*configDir)
			if err != nil {
				return err
			}
			cfg, err := console.LoadConfig(base)
			if err != nil {
				return err
			}
			if len(args) == 0 {
				fmt.Fprintf(out, "Enter on controller connect: %s\n", onOff(cfg.EnterOnControllerConnect))
				return nil
			}
			cfg.EnterOnControllerConnect = args[0] == "on"
			if err := console.SaveConfig(base, cfg); err != nil {
				return err
			}
			if cfg.EnterOnControllerConnect {
				fmt.Fprintln(out, "Switching a controller on will now close your desktop session.")
				fmt.Fprintln(out, "You get 20 seconds and a notification you can click to call it off;")
				fmt.Fprintln(out, "switching the controller back off stops it too, and so does")
				fmt.Fprintln(out, "`hyprmoncfg console cancel`.")
			} else {
				fmt.Fprintln(out, "Controllers no longer start a console session.")
			}
			return nil
		},
	}
}

// newConsoleBootCmd chooses where a fresh login lands.
//
// A console that boots to a desktop with an icon on it is not really a console.
// But the desktop stays the default, because somebody who installs this to play
// on the TV occasionally should not have their computer stop presenting one.
func newConsoleBootCmd(configDir *string) *cobra.Command {
	return &cobra.Command{
		Use:       "boot [desktop|console|last]",
		Short:     "Choose where the machine lands when it starts",
		Args:      cobra.MaximumNArgs(1),
		ValidArgs: []string{"desktop", "console", "last"},
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			base, err := config.EnsureBaseDir(*configDir)
			if err != nil {
				return err
			}
			cfg, err := console.LoadConfig(base)
			if err != nil {
				return err
			}
			if len(args) == 0 {
				fmt.Fprintf(out, "Starts in: %s\n", cfg.Boot)
				fmt.Fprintln(out, "\n  desktop  always the desktop")
				fmt.Fprintln(out, "  console  always the console, the way a games machine does")
				fmt.Fprintln(out, "  last     wherever the last session ended")
				return nil
			}
			mode := console.BootMode(args[0])
			if !mode.Valid() {
				return fmt.Errorf("unknown boot mode %q: use desktop, console or last", args[0])
			}
			cfg.Boot = mode
			if err := console.SaveConfig(base, cfg); err != nil {
				return err
			}
			if note := bootLoginNote(cmd.Context(), mode); note != "" {
				fmt.Fprintf(out, "\nNote: %s\n", note)
			}
			switch mode {
			case console.BootConsole:
				fmt.Fprintln(out, "The machine will start in console mode.")
				fmt.Fprintln(out, "Leave it from Big Picture: Steam -> Power -> Switch to Desktop.")
			case console.BootLast:
				fmt.Fprintln(out, "The machine will start wherever the last session ended.")
			default:
				fmt.Fprintln(out, "The machine will start at the desktop.")
			}
			fmt.Fprintln(out, "\nIf the console cannot start, the desktop comes up instead.")
			return nil
		},
	}
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

// bootLoginNote warns when the machine will stop for a password on its way to a
// console.
//
// The greeter comes up on whatever display it uses -- normally the desk monitor --
// while the person waiting is on the sofa. The machine still works; it just is
// not a console, and that is worth hearing before choosing rather than after
// the first boot.
func bootLoginNote(ctx context.Context, boot console.BootMode) string {
	if boot != console.BootConsole && boot != console.BootLast {
		return ""
	}
	lm := console.DetectLoginManager(ctx, console.Systemctl{})
	switch console.HasAutologin(lm) {
	case console.AutologinOff:
		return "the login manager will ask for a password before the console starts;\n      turn its autologin on if you want the machine to come up on the TV"
	case console.AutologinUnknown:
		return fmt.Sprintf("cannot tell whether %s logs in automatically; if it does not, it will\n      ask for a password on its own display before the console starts", lm.Kind)
	default:
		return ""
	}
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
