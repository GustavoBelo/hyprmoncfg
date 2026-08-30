package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/crmne/hyprmoncfg/internal/buildinfo"
	"github.com/crmne/hyprmoncfg/internal/config"
	"github.com/crmne/hyprmoncfg/internal/couch"
	"github.com/crmne/hyprmoncfg/internal/couch/hooks"
	"github.com/crmne/hyprmoncfg/internal/hypr"
	"github.com/crmne/hyprmoncfg/internal/ipc"
	"github.com/crmne/hyprmoncfg/internal/profile"
)

func newCouchCmd(configDir *string, monitorsConf *string, hyprConfig *string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "couch",
		Short: "Couch Mode: switch to a TV layout and run Steam Big Picture sessions",
		Long: "Couch Mode turns the machine into a console: it applies a generated TV\n" +
			"layout, launches Steam Big Picture, and puts the desktop back when the\n" +
			"session ends. Enable it with `couch enable`, then tune the layout with\n" +
			"`hyprmoncfg` (tab 4).",
		// These commands fail for reasons the user has to act on -- monitor
		// config handed back, no TV connected -- and repeating the flag list
		// under each one buries the sentence that matters.
		SilenceUsage: true,
	}
	cmd.AddCommand(
		newCouchEnableCmd(configDir),
		newCouchDisableCmd(configDir),
		newCouchStatusCmd(configDir),
		newCouchPlayCmd(configDir, monitorsConf, hyprConfig),
		newCouchRestoreCmd(configDir, monitorsConf, hyprConfig),
		newCouchStopCmd(configDir),
		newCouchOutputsCmd(),
		newCouchDoctorCmd(configDir),
		newCouchVersionCmd(),
		newCouchLogCmd(),
		newCouchAppsCmd(configDir),
	)
	// Cobra consults the root or the executed command, never an intermediate
	// parent, so the flag has to be pushed down the tree.
	silenceUsageTree(cmd)
	return cmd
}

func silenceUsageTree(cmd *cobra.Command) {
	cmd.SilenceUsage = true
	for _, child := range cmd.Commands() {
		silenceUsageTree(child)
	}
}

func newCouchEnableCmd(configDir *string) *cobra.Command {
	return &cobra.Command{
		Use:   "enable",
		Short: "Enable Couch Mode and generate its console profile",
		RunE: func(cmd *cobra.Command, args []string) error {
			base, err := config.EnsureBaseDir(*configDir)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()

			// A session writes the console layout through the normal apply
			// path, which repairs hyprmoncfg's include as a side effect. Doing
			// that while the user has handed monitor config back would take
			// control again behind their back -- which is exactly how this
			// machine ended up with an `unmanaged` marker and hyprmoncfg's
			// generated file still winning.
			if !config.IsManaged(base) {
				fmt.Fprintln(cmd.ErrOrStderr(),
					"Couch Mode needs hyprmoncfg to be managing monitor configuration.")
				fmt.Fprintln(cmd.ErrOrStderr(),
					"Save your current layout first so nothing is lost, then hand control over:")
				fmt.Fprint(cmd.ErrOrStderr(), "\n  hyprmoncfg save <name>\n  hyprmoncfg manage\n\n")
				return errors.New("monitor configuration is handed back to Hyprland")
			}

			cfg, err := couch.LoadConfig(base)
			if err != nil {
				return err
			}
			cfg.Enabled = true

			client, err := hypr.NewClient()
			if err != nil {
				return err
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), 10*time.Second)
			defer cancel()
			monitors, err := client.Monitors(ctx)
			if err != nil {
				return err
			}

			store := profile.NewStore(base)
			if err := store.Ensure(); err != nil {
				return err
			}
			built, _, err := couch.EnsureConsoleProfile(store, monitors, &cfg)
			if err != nil {
				return fmt.Errorf("prepare the console layout: %w", err)
			}
			if err := couch.SaveConfig(base, cfg); err != nil {
				return err
			}

			fmt.Fprintln(out, "Couch Mode enabled.")
			fmt.Fprintf(out, "Console profile %q: TV %s at %s", built.Name, cfg.Layout.TVName, cfg.Layout.Mode)
			if cfg.Layout.HDR {
				fmt.Fprint(out, ", HDR")
			}
			if cfg.Layout.VRR {
				fmt.Fprint(out, ", VRR")
			}
			fmt.Fprintf(out, "; other displays %s.\n", cfg.Layout.Desk)
			fmt.Fprintln(out, "Adjust it on tab 4 of `hyprmoncfg`, or start a session with `hyprmoncfg couch play`.")

			if len(monitors) < 2 {
				fmt.Fprintln(out, "Note: only one display is connected right now.")
			}
			return nil
		},
	}
}

func newCouchDisableCmd(configDir *string) *cobra.Command {
	return &cobra.Command{
		Use:   "disable",
		Short: "Disable Couch Mode",
		RunE: func(cmd *cobra.Command, args []string) error {
			base, err := config.EnsureBaseDir(*configDir)
			if err != nil {
				return err
			}
			cfg, err := couch.LoadConfig(base)
			if err != nil {
				return err
			}
			cfg.Enabled = false
			if err := couch.SaveConfig(base, cfg); err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			fmt.Fprintln(out, "Couch Mode disabled.")
			state, err := couch.StateDir()
			if err == nil {
				if _, running := couch.RunningSession(state); running {
					fmt.Fprintln(out, "A session is still running; it will restore the desk profile when it ends.")
				}
			}
			return nil
		},
	}
}

func newCouchStatusCmd(configDir *string) *cobra.Command {
	var jsonOutput bool

	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show Couch Mode configuration and session state",
		RunE: func(cmd *cobra.Command, args []string) error {
			base, err := config.EnsureBaseDir(*configDir)
			if err != nil {
				return err
			}
			report, err := couch.BuildStatus(base)
			if err != nil {
				return err
			}
			if jsonOutput {
				encoder := json.NewEncoder(cmd.OutOrStdout())
				encoder.SetIndent("", "  ")
				return encoder.Encode(report)
			}

			out := cmd.OutOrStdout()
			stateText := "disabled"
			if report.Enabled {
				stateText = "enabled"
			}
			fmt.Fprintf(out, "Couch Mode: %s\n", stateText)
			if !report.Managed {
				fmt.Fprintln(out, "Monitor config: handed back to Hyprland — run `hyprmoncfg manage` before playing")
			}
			fmt.Fprintf(out, "TV display: %s\n", orDash(report.Layout.TVName))
			fmt.Fprintf(out, "TV mode: %s%s%s\n", orDash(report.Layout.Mode),
				flagSuffix(report.Layout.HDR, " HDR"), flagSuffix(report.Layout.VRR, " VRR"))
			fmt.Fprintf(out, "Other displays: %s\n", report.Layout.Desk)
			fmt.Fprintf(out, "Exit when controllers turn off: %s\n", onOff(report.ExitOnControllersOff))
			fmt.Fprintf(out, "Controllers connected: %d\n", report.Controllers)
			fmt.Fprintf(out, "Close apps during play: %s\n", onOff(report.CloseAppsEnabled))
			sessionState := "inactive"
			if report.Running {
				sessionState = fmt.Sprintf("running (PID %d)", report.PID)
				if report.Duration != "" {
					sessionState += " for " + report.Duration
				}
			} else if report.Stale {
				sessionState = fmt.Sprintf("stale (PID %d died) — run `hyprmoncfg couch restore` to recover", report.PID)
			}
			fmt.Fprintf(out, "Session: %s\n", sessionState)
			for _, line := range report.RecentLog {
				fmt.Fprintf(out, "  %s\n", line)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Print machine-readable JSON")
	return cmd
}

func newCouchPlayCmd(configDir *string, monitorsConf *string, hyprConfig *string) *cobra.Command {
	return &cobra.Command{
		Use:   "play",
		Short: "Enter console mode: apply the TV layout and launch Big Picture",
		Long: "Applies the generated console layout, launches Steam Big Picture, and\n" +
			"puts the desktop back when Big Picture closes, Steam exits, every\n" +
			"controller turns off (if enabled), or the session is stopped.\n\n" +
			"With the daemon running the session lives there, so it survives this\n" +
			"command exiting and is recovered if the daemon is killed.",
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			if handled, err := couchStartViaDaemon(cmd.Context(), "the `couch play` command"); handled {
				if err != nil {
					return err
				}
				fmt.Fprintln(out, "Console mode started; the daemon owns the session.")
				fmt.Fprintln(out, "Stop it with `hyprmoncfg couch stop`.")
				return nil
			}

			runner, err := newCouchRunner(*configDir, *monitorsConf, *hyprConfig)
			if err != nil {
				return err
			}
			fmt.Fprintln(out, "No daemon running; holding the session in this process.")
			fmt.Fprintln(out, "Starting console mode...")
			if err := runner.Play(cmd.Context()); err != nil {
				return err
			}
			fmt.Fprintln(out, "Session finished; desktop layout restored.")
			return nil
		},
	}
}

// couchStartViaDaemon hands the session to the daemon when one is listening.
//
// The daemon holds the write lock and the event feed, so a session there cannot
// race the automatic switching the way a detached CLI child did.
func couchStartViaDaemon(ctx context.Context, trigger string) (bool, error) {
	client, ok := dialDaemon(ctx)
	if !ok {
		return false, nil
	}
	defer client.Close()
	return true, client.CouchStart(ctx, trigger)
}

func couchStopViaDaemon(ctx context.Context) (bool, error) {
	client, ok := dialDaemon(ctx)
	if !ok {
		return false, nil
	}
	defer client.Close()
	return true, client.CouchStop(ctx)
}

func couchStatusViaDaemon(ctx context.Context) (ipc.CouchState, bool) {
	client, ok := dialDaemon(ctx)
	if !ok {
		return ipc.CouchState{}, false
	}
	defer client.Close()
	state, err := client.CouchStatus(ctx)
	if err != nil {
		return ipc.CouchState{}, false
	}
	return state, true
}

func dialDaemon(ctx context.Context) (*ipc.Client, bool) {
	path, err := ipc.SocketPath()
	if err != nil {
		return nil, false
	}
	dialCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	client, err := ipc.Dial(dialCtx, path)
	if err != nil {
		return nil, false
	}
	return client, true
}

func newCouchRestoreCmd(configDir *string, monitorsConf *string, hyprConfig *string) *cobra.Command {
	return &cobra.Command{
		Use:   "restore",
		Short: "Apply the desk profile without starting a session",
		RunE: func(cmd *cobra.Command, args []string) error {
			runner, err := newCouchRunner(*configDir, *monitorsConf, *hyprConfig)
			if err != nil {
				return err
			}
			if err := runner.Restore(cmd.Context()); err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), "Desk profile applied.")
			return nil
		},
	}
}

func newCouchStopCmd(configDir *string) *cobra.Command {
	return &cobra.Command{
		Use:   "stop",
		Short: "Stop the running console session",
		RunE: func(cmd *cobra.Command, args []string) error {
			base, err := config.EnsureBaseDir(*configDir)
			if err != nil {
				return err
			}
			if handled, err := couchStopViaDaemon(cmd.Context()); handled {
				if err != nil {
					fmt.Fprintln(cmd.ErrOrStderr(), err)
					return nil
				}
				fmt.Fprintln(cmd.OutOrStdout(), "Session stopped; the desktop layout is being restored.")
				return nil
			}
			runner := couch.Runner{BaseDir: base}
			if err := runner.Stop(); err != nil {
				fmt.Fprintln(cmd.ErrOrStderr(), err)
				return nil
			}
			fmt.Fprintln(cmd.OutOrStdout(), "Session stopped.")
			return nil
		},
	}
}

func newCouchRunner(configDir string, monitorsConf string, hyprConfig string) (couch.Runner, error) {
	base, err := config.EnsureBaseDir(configDir)
	if err != nil {
		return couch.Runner{}, err
	}
	client, err := hypr.NewClient()
	if err != nil {
		return couch.Runner{}, err
	}
	store := profile.NewStore(base)
	if err := store.Ensure(); err != nil {
		return couch.Runner{}, err
	}
	return couch.Runner{
		Client:             client,
		Store:              store,
		BaseDir:            base,
		MonitorsConfPath:   monitorsConf,
		HyprlandConfigPath: hyprConfig,
		Logf: func(format string, args ...any) {
			fmt.Printf(format+"\n", args...)
		},
	}, nil
}

func orDash(value string) string {
	if value == "" {
		return "-"
	}
	return value
}

func flagSuffix(on bool, label string) string {
	if on {
		return label
	}
	return ""
}

func onOff(value bool) string {
	if value {
		return "on"
	}
	return "off"
}

func newCouchOutputsCmd() *cobra.Command {
	var jsonOutput bool

	cmd := &cobra.Command{
		Use:   "outputs",
		Short: "List connected display outputs and their modes",
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := hypr.NewClient()
			if err != nil {
				return err
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), 5*time.Second)
			defer cancel()
			monitors, err := client.Monitors(ctx)
			if err != nil {
				return err
			}
			if jsonOutput {
				encoder := json.NewEncoder(cmd.OutOrStdout())
				encoder.SetIndent("", "  ")
				return encoder.Encode(monitors)
			}
			out := cmd.OutOrStdout()
			for _, m := range monitors {
				state := "on"
				if m.Disabled {
					state = "off"
				}
				mode := fmt.Sprintf("%dx%d@%.2f", m.Width, m.Height, m.RefreshRate)
				if m.Width == 0 || m.Height == 0 {
					mode = "preferred"
				}
				fmt.Fprintf(out, "%-12s %-4s %-16s %dx%d @%.2f  key=%s\n",
					m.Name, state, mode, m.X, m.Y, m.Scale, m.HardwareKey())
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Print machine-readable JSON")
	return cmd
}

// newCouchDoctorCmd reports whether a console session would actually work.
//
// The previous `couch check` looped over a list of dependency names but called
// hypr.NewClient() for each one, so the names were decorative and it could only
// ever say "hyprctl". Everything that has bitten a session -- monitor config
// handed back, a TV that is not connected, a mode the display withdrew, no
// Steam -- is checked here instead.
func newCouchDoctorCmd(configDir *string) *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Check that a console session would work",
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			problems := 0
			report := func(ok bool, required bool, format string, a ...any) {
				mark := "ok  "
				switch {
				case ok:
				case required:
					mark = "FAIL"
					problems++
				default:
					mark = "warn"
				}
				fmt.Fprintf(out, "%s  %s\n", mark, fmt.Sprintf(format, a...))
			}

			base, err := config.EnsureBaseDir(*configDir)
			if err != nil {
				return err
			}
			cfg, err := couch.LoadConfig(base)
			if err != nil {
				return err
			}

			report(cfg.Enabled, true, "Couch Mode is %s", enabledWord(cfg.Enabled))
			report(config.IsManaged(base), true,
				"monitor configuration is %s", managedWord(config.IsManaged(base)))

			client, err := hypr.NewClient()
			if err != nil {
				report(false, true, "Hyprland: %v", err)
				return doctorResult(out, problems)
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), 5*time.Second)
			defer cancel()
			monitors, err := client.Monitors(ctx)
			if err != nil {
				report(false, true, "could not read displays: %v", err)
				return doctorResult(out, problems)
			}
			report(true, true, "Hyprland reachable, %d display(s)", len(monitors))

			if !cfg.Configured() {
				report(false, true, "no console layout yet; run `hyprmoncfg couch enable`")
			} else if err := couch.ValidateConsoleLayout(cfg.Layout, monitors); err != nil {
				report(false, true, "console layout: %v", err)
			} else {
				report(true, true, "console layout: %s at %s%s%s", cfg.Layout.TVName, cfg.Layout.Mode,
					flagSuffix(cfg.Layout.HDR, " +HDR"), flagSuffix(cfg.Layout.VRR, " +VRR"))
				if cfg.Layout.HDR && !hypr.HDRCapableConnectors()[cfg.Layout.TVName] {
					report(false, false, "%s does not advertise HDR in its EDID", cfg.Layout.TVName)
				}
				for _, mon := range monitors {
					if mon.HardwareKey() != cfg.Layout.TVKey {
						continue
					}
					if native, ok := couch.ModeMatchesPanelShape(cfg.Layout.Mode, mon, couch.LiveDisplayFacts()); !ok {
						report(false, false,
							"%s is not the shape of the panel; %s is native, so this one shows black bars",
							cfg.Layout.Mode, native)
					}
				}
				// Mirroring puts both outputs at 0,0 by definition, so a client
				// enumerating displays sees two of them on the same coordinates
				// at different sizes. Steam Big Picture reads that as a broken
				// arrangement and says so on every launch.
				if cfg.Layout.Desk == couch.DeskMirror {
					report(false, false,
						"the desk mirrors the TV, so both displays sit at 0,0 and Steam warns the arrangement is wrong; turn the desk off during play to stop it")
				}
			}

			if _, err := exec.LookPath("steam"); err != nil {
				if _, bazzite := exec.LookPath("bazzite-steam-bpm"); bazzite != nil {
					report(false, true, "Steam is not installed")
				} else {
					report(true, true, "Big Picture launcher: bazzite-steam-bpm")
				}
			} else {
				report(true, true, "Steam is installed")
			}

			if !couch.GamescopeAvailable() {
				report(false, false, "gamescope is not installed (optional: per-game HDR, FSR, fps cap)")
				if cfg.Gamescope.Enabled {
					report(false, true, "gamescope is switched on but not installed")
				}
			} else {
				report(true, false, "gamescope is installed (%s)", couch.GamescopeSummary(cfg.Layout, cfg.Gamescope))
			}

			for _, hook := range hooks.All() {
				switch {
				case !hook.Available():
					report(false, false, "%s: unavailable on this machine", hook.Description())
				case !cfg.HookEnabled(hook.Name()):
					report(true, false, "%s: turned off", hook.Description())
				default:
					report(true, false, "%s: on", hook.Description())
				}
			}

			// Runtime option changes are refused on Hyprland's Lua config
			// parser -- both `hyprctl keyword` and hl.config() are accepted and
			// then ignored -- so tearing has to be set in the config once,
			// by the user, rather than switched on per session.
			if !tearingEnabled(ctx, client) {
				report(false, false, "general:allow_tearing is off; set it in your Hyprland config for lower latency in games")
			} else {
				report(true, false, "general:allow_tearing is on")
			}

			report(couch.ConnectedControllers() > 0, false,
				"%d controller(s) connected", couch.ConnectedControllers())

			// Everything above answers for this shell. The daemon is what
			// actually starts a session when a trigger fires, and it sees a
			// different world: a user unit inherits no Wayland socket, no X11
			// display and a PATH without the Omarchy helpers. Every check that
			// mattered passed from a terminal while automatic entry launched a
			// Steam that exited at once, so ask the daemon itself.
			reportDaemonSession(ctx, report)

			return doctorResult(out, problems)
		},
	}
}

// tearingEnabled reads the live option. It is only ever reported, never set:
// see the note at the call site.
func tearingEnabled(ctx context.Context, client *hypr.Client) bool {
	value, err := client.Option(ctx, "general:allow_tearing")
	if err != nil {
		return false
	}
	return value.Bool
}

func doctorResult(out io.Writer, problems int) error {
	fmt.Fprintln(out)
	if problems == 0 {
		fmt.Fprintln(out, "Ready for a console session.")
		return nil
	}
	return fmt.Errorf("%d problem(s) would stop a console session", problems)
}

func enabledWord(on bool) string {
	if on {
		return "enabled"
	}
	return "disabled — run `hyprmoncfg couch enable`"
}

func managedWord(managed bool) string {
	if managed {
		return "managed by hyprmoncfg"
	}
	return "handed back to Hyprland — run `hyprmoncfg manage`"
}

func newCouchVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Show the couch module version",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Fprintf(cmd.OutOrStdout(), "couch mode, built into hyprmoncfg %s\n", buildinfo.Version)
		},
	}
}

func newCouchAppsCmd(configDir *string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "apps",
		Short: "Discover closeable applications",
	}

	suggestCmd := &cobra.Command{
		Use:   "suggest",
		Short: "List what a session can be told to close",
		Long: "Lists open windows first, then installed applications. The name in\n" +
			"brackets is what gets stored: matching is exact, so it is the window\n" +
			"class or process name, never a title.",
		RunE: func(cmd *cobra.Command, args []string) error {
			base, err := config.EnsureBaseDir(*configDir)
			if err != nil {
				return err
			}
			cfg, err := couch.LoadConfig(base)
			if err != nil {
				return err
			}

			var source couch.WindowSource
			if client, err := hypr.NewClient(); err == nil {
				source = client
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), 5*time.Second)
			defer cancel()
			candidates := couch.CloseCandidates(ctx, source)
			if len(candidates) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "Nothing to offer: no windows are open and no applications were found.")
				return nil
			}

			jsonOut, _ := cmd.Flags().GetBool("json")
			if jsonOut {
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				return enc.Encode(candidates)
			}

			chosen := couch.MarkChosen(candidates, cfg.AppsToClose)
			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "Apps a session can close (%d found):\n\n", len(candidates))
			for _, c := range candidates {
				mark := " "
				if chosen[strings.ToLower(c.Token)] {
					mark = "*"
				}
				where := ""
				if c.Running {
					where = "  (open now)"
				}
				fmt.Fprintf(out, " %s %-34s %s%s\n", mark, c.Token, c.Label, where)
			}
			fmt.Fprintln(out)
			fmt.Fprintln(out, "* is already on the list. Pick them on tab 4 of `hyprmoncfg`,")
			fmt.Fprintln(out, "or add one by name with `hyprmoncfg couch apps add <name>`.")
			return nil
		},
	}

	runningCmd := &cobra.Command{
		Use:   "running",
		Short: "Show running apps that match the tracked close list",
		RunE: func(cmd *cobra.Command, args []string) error {
			base, err := config.EnsureBaseDir(*configDir)
			if err != nil {
				return err
			}
			cfg, err := couch.LoadConfig(base)
			if err != nil {
				return err
			}
			running := couch.RunningApps(cfg.AppsToClose)
			if len(running) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "No tracked apps are currently running.")
				return nil
			}
			jsonOut, _ := cmd.Flags().GetBool("json")
			if jsonOut {
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				return enc.Encode(running)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Running tracked apps (%d):\n\n", len(running))
			for _, a := range running {
				fmt.Fprintf(cmd.OutOrStdout(), "  %-12s  PID %d\n", a.Name, a.PID)
			}
			return nil
		},
	}

	suggestCmd.Flags().Bool("json", false, "Print machine-readable JSON")
	runningCmd.Flags().Bool("json", false, "Print machine-readable JSON")
	cmd.AddCommand(suggestCmd, runningCmd, newCouchAppsAddCmd(configDir), newCouchAppsRemoveCmd(configDir))
	return cmd
}

// newCouchAppsAddCmd and its remove twin close the loop that `apps suggest`
// already pointed at: it told users to run `couch apps add`, which did not exist.
func newCouchAppsAddCmd(configDir *string) *cobra.Command {
	return &cobra.Command{
		Use:   "add <name>...",
		Short: "Add processes to the close list",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return mutateCloseList(cmd, *configDir, func(current []string) []string {
				return append(current, args...)
			})
		},
	}
}

func newCouchAppsRemoveCmd(configDir *string) *cobra.Command {
	return &cobra.Command{
		Use:   "remove <name>...",
		Short: "Remove processes from the close list",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			drop := make(map[string]struct{}, len(args))
			for _, name := range args {
				drop[strings.ToLower(strings.TrimSpace(name))] = struct{}{}
			}
			return mutateCloseList(cmd, *configDir, func(current []string) []string {
				kept := make([]string, 0, len(current))
				for _, name := range current {
					if _, remove := drop[strings.ToLower(name)]; !remove {
						kept = append(kept, name)
					}
				}
				return kept
			})
		},
	}
}

func mutateCloseList(cmd *cobra.Command, configDir string, mutate func([]string) []string) error {
	base, err := config.EnsureBaseDir(configDir)
	if err != nil {
		return err
	}
	cfg, err := couch.LoadConfig(base)
	if err != nil {
		return err
	}
	cfg.AppsToClose = couch.SanitizeApps(mutate(cfg.AppsToClose))
	if err := couch.SaveConfig(base, cfg); err != nil {
		return err
	}
	if len(cfg.AppsToClose) == 0 {
		fmt.Fprintln(cmd.OutOrStdout(), "Close list is now empty.")
		return nil
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Close list: %s\n", strings.Join(cfg.AppsToClose, ", "))
	return nil
}

func newCouchLogCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "log",
		Short: "Manage couch mode logs",
	}
	cmd.AddCommand(
		&cobra.Command{
			Use:   "show",
			Short: "Show recent log lines",
			RunE: func(cmd *cobra.Command, args []string) error {
				state, err := couch.StateDir()
				if err != nil {
					return err
				}
				lines := couch.LogTail(state, 200)
				for _, line := range lines {
					fmt.Fprintln(cmd.OutOrStdout(), line)
				}
				return nil
			},
		},
		&cobra.Command{
			Use:   "clear",
			Short: "Archive and clear the current log",
			RunE: func(cmd *cobra.Command, args []string) error {
				state, err := couch.StateDir()
				if err != nil {
					return err
				}
				couch.ClearLog(state)
				fmt.Fprintln(cmd.OutOrStdout(), "Log archived and cleared.")
				return nil
			},
		},
		&cobra.Command{
			Use:   "history",
			Short: "List archived log files",
			RunE: func(cmd *cobra.Command, args []string) error {
				state, err := couch.StateDir()
				if err != nil {
					return err
				}
				logs := couch.ListHistoryLogs(state)
				if len(logs) == 0 {
					fmt.Fprintln(cmd.OutOrStdout(), "No archived logs.")
					return nil
				}
				for _, name := range logs {
					fmt.Fprintln(cmd.OutOrStdout(), name)
				}
				return nil
			},
		},
	)
	return cmd
}

// reportDaemonSession asks the running daemon what it cannot reach.
//
// A silent daemon is not a failure here: a session started by hand from this
// shell works either way, and `couch status` already says whether the daemon is
// up.
func reportDaemonSession(ctx context.Context, report func(bool, bool, string, ...any)) {
	state, ok := couchStatusViaDaemon(ctx)
	if !ok {
		return
	}
	if len(state.MissingSessionEnv) > 0 {
		report(false, true, "the daemon cannot see the graphical session (%s); automatic entry will start a session Steam cannot join",
			strings.Join(state.MissingSessionEnv, ", "))
	} else {
		report(true, false, "the daemon can see the graphical session")
	}
	if len(state.UnavailableHooks) > 0 {
		report(false, true, "the daemon cannot run these enabled hooks: %s", strings.Join(state.UnavailableHooks, ", "))
	}
}
