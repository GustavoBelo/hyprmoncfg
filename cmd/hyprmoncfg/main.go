package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/spf13/cobra"

	"github.com/crmne/hyprmoncfg/internal/apply"
	"github.com/crmne/hyprmoncfg/internal/buildinfo"
	"github.com/crmne/hyprmoncfg/internal/config"
	"github.com/crmne/hyprmoncfg/internal/hypr"
	"github.com/crmne/hyprmoncfg/internal/lid"
	"github.com/crmne/hyprmoncfg/internal/profile"
	"github.com/crmne/hyprmoncfg/internal/profileio"
	"github.com/crmne/hyprmoncfg/internal/tui"
)

func main() {
	if err := newRootCmd().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(1)
	}
}

func newRootCmd() *cobra.Command {
	var configDir string
	var monitorsConf string
	var hyprConfig string

	root := &cobra.Command{
		Use:     "hyprmoncfg",
		Short:   "Monitor profile manager for Hyprland",
		Version: buildinfo.Version,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runTUI(configDir, monitorsConf, hyprConfig)
		},
	}
	root.PersistentFlags().StringVar(&configDir, "config-dir", "", "Config directory (default: ~/.config/hyprmoncfg)")
	root.PersistentFlags().StringVar(&monitorsConf, "monitors-conf", "", "Generated monitor config target to write and reload (overrides HYPRMONCFG_MONITORS_CONF)")
	root.PersistentFlags().StringVar(&hyprConfig, "hypr-config", "", "Hyprland root config for include verification (overrides HYPRLAND_CONFIG)")

	root.AddCommand(newTUICmd(&configDir, &monitorsConf, &hyprConfig))
	root.AddCommand(newMonitorsCmd(&configDir))
	root.AddCommand(newProfilesCmd(&configDir))
	root.AddCommand(newSaveCmd(&configDir))
	root.AddCommand(newApplyCmd(&configDir, &monitorsConf, &hyprConfig))
	root.AddCommand(newDeleteCmd(&configDir))
	root.AddCommand(newVersionCmd("hyprmoncfg"))

	return root
}

func newTUICmd(configDir *string, monitorsConf *string, hyprConfig *string) *cobra.Command {
	return &cobra.Command{
		Use:   "tui",
		Short: "Launch interactive terminal UI",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runTUI(*configDir, *monitorsConf, *hyprConfig)
		},
	}
}

func newMonitorsCmd(configDir *string) *cobra.Command {
	return &cobra.Command{
		Use:   "monitors",
		Short: "List current monitors from Hyprland",
		RunE: func(cmd *cobra.Command, args []string) error {
			client, _, err := bootstrap(*configDir)
			if err != nil {
				return err
			}

			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			monitors, err := client.Monitors(ctx)
			if err != nil {
				return err
			}

			w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
			fmt.Fprintln(w, "NAME\tSTATE\tMODE\tPOSITION\tSCALE\tKEY")
			for _, m := range monitors {
				state := "on"
				if m.Disabled {
					state = "off"
				}
				mode := fmt.Sprintf("%dx%d@%.2f", m.Width, m.Height, m.RefreshRate)
				if m.Width == 0 || m.Height == 0 {
					mode = "preferred"
				}
				fmt.Fprintf(w, "%s\t%s\t%s\t%dx%d\t%.2f\t%s\n", m.Name, state, mode, m.X, m.Y, m.Scale, m.HardwareKey())
			}
			return w.Flush()
		},
	}
}

func newProfilesCmd(configDir *string) *cobra.Command {
	return &cobra.Command{
		Use:   "profiles",
		Short: "List saved profiles",
		RunE: func(cmd *cobra.Command, args []string) error {
			_, store, err := bootstrap(*configDir)
			if err != nil {
				return err
			}
			profiles, err := store.List()
			if err != nil {
				return err
			}
			if len(profiles) == 0 {
				fmt.Println("No saved profiles")
				return nil
			}
			w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
			fmt.Fprintln(w, "NAME\tOUTPUTS\tUPDATED")
			for _, p := range profiles {
				fmt.Fprintf(w, "%s\t%d\t%s\n", p.Name, len(p.Outputs), p.UpdatedAt.Local().Format(time.RFC3339))
			}
			return w.Flush()
		},
	}
}

func newSaveCmd(configDir *string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "save <name>",
		Short: "Save current monitor state as profile",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			client, store, err := bootstrap(*configDir)
			if err != nil {
				return err
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			monitors, err := client.Monitors(ctx)
			if err != nil {
				return err
			}
			rules, err := client.WorkspaceRules(ctx)
			if err != nil {
				return err
			}
			p := profile.FromState(name, monitors, rules)
			existing, err := store.Load(name)
			if err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
			p.Exec = existing.Exec
			if err := profileio.SaveWithSidecars(store, p); err != nil {
				return err
			}
			fmt.Printf("Saved profile %q\n", p.Name)
			return nil
		},
	}
	return cmd
}

func newApplyCmd(configDir *string, monitorsConf *string, hyprConfig *string) *cobra.Command {
	var confirmTimeout int

	cmd := &cobra.Command{
		Use:   "apply <name>",
		Short: "Apply a saved profile",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, store, err := bootstrap(*configDir)
			if err != nil {
				return err
			}
			p, err := store.Load(args[0])
			if err != nil {
				return err
			}

			ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
			defer cancel()
			monitors, err := client.Monitors(ctx)
			if err != nil {
				return err
			}
			applyProfile := p
			if state, err := lid.ReadState(ctx); err == nil && state == lid.Closed {
				applyProfile, _ = profile.ApplyClosedLidPolicy(p, monitors)
			}

			isInteractive := confirmTimeout > 0
			var applySignals chan os.Signal
			if isInteractive {
				applySignals = make(chan os.Signal, 1)
				signal.Notify(applySignals, os.Interrupt, syscall.SIGTERM)
				defer signal.Stop(applySignals)
			}

			engine := apply.Engine{
				Client:             client,
				MonitorsConfPath:   *monitorsConf,
				HyprlandConfigPath: *hyprConfig,
				Logf: func(format string, args ...any) {
					fmt.Printf(format, args...)
				},
			}
			snapshot, err := engine.Apply(ctx, applyProfile, monitors, apply.ApplyModeInteractive)
			if err != nil {
				var unmanaged *apply.UnmanagedMonitorConfigError
				if !isInteractive || !errors.As(err, &unmanaged) {
					return err
				}

				overwrite, promptErr := confirmUnmanagedOverwrite(cmd.InOrStdin(), cmd.OutOrStdout(), applySignals, unmanaged)
				if promptErr != nil {
					return fmt.Errorf("read overwrite confirmation: %w", promptErr)
				}
				if !overwrite {
					fmt.Fprintln(cmd.OutOrStdout(), "Existing monitor config left unchanged")
					return nil
				}

				engine.AllowUnmanagedOverwrite = true
				retryCtx, retryCancel := context.WithTimeout(context.Background(), 8*time.Second)
				defer retryCancel()
				snapshot, err = engine.Apply(retryCtx, applyProfile, monitors, apply.ApplyModeInteractive)
				if err != nil {
					return err
				}
			}
			fmt.Printf("Applied profile %q\n", p.Name)

			if !isInteractive {
				postApplyCtx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
				defer cancel()
				if err := engine.PostApply(postApplyCtx, applyProfile); err != nil {
					fmt.Printf("Post-apply failed for %s: %v\n", p.Name, err)
				}
				return nil
			}

			keep, err := confirmApplyWithInput(confirmTimeout, cmd.InOrStdin(), cmd.OutOrStdout(), applySignals)
			if keep {
				fmt.Println("Configuration kept")

				postApplyCtx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
				defer cancel()

				err = engine.PostApply(postApplyCtx, applyProfile)
				if err != nil {
					fmt.Printf("Post-apply failed for %s: %v\n", p.Name, err)
				}

				return nil
			}

			revertCtx, revertCancel := context.WithTimeout(context.Background(), 8*time.Second)
			defer revertCancel()
			revertErr := engine.Revert(revertCtx, snapshot)
			if revertErr != nil {
				revertErr = fmt.Errorf("failed to revert unconfirmed configuration: %w", revertErr)
				if err != nil {
					return errors.Join(err, revertErr)
				}
				return revertErr
			}
			fmt.Println("Configuration reverted")
			return err
		},
	}
	cmd.Flags().IntVar(&confirmTimeout, "confirm-timeout", 10, "Seconds to confirm configuration before reverting; set 0 to disable")
	return cmd
}

func newDeleteCmd(configDir *string) *cobra.Command {
	return &cobra.Command{
		Use:   "delete <name>",
		Short: "Delete saved profile",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			_, store, err := bootstrap(*configDir)
			if err != nil {
				return err
			}
			if err := store.Delete(args[0]); err != nil {
				return err
			}
			fmt.Printf("Deleted profile %q\n", args[0])
			return nil
		},
	}
}

func newVersionCmd(name string) *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Show build version information",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Fprintln(cmd.OutOrStdout(), buildinfo.Summary(name))
		},
	}
}

func runTUI(configDir string, monitorsConf string, hyprConfig string) error {
	client, store, err := bootstrap(configDir)
	if err != nil {
		return err
	}

	model := tui.NewModel(client, store, monitorsConf, hyprConfig)
	p := tea.NewProgram(model, tea.WithAltScreen(), tea.WithMouseCellMotion())
	_, runErr := p.Run()

	revertCtx, revertCancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer revertCancel()
	if revertErr := model.RevertPending(revertCtx); revertErr != nil {
		revertErr = fmt.Errorf("failed to revert unconfirmed configuration while quitting: %w", revertErr)
		if runErr != nil {
			return errors.Join(runErr, revertErr)
		}
		return revertErr
	}
	return runErr
}

func bootstrap(explicitConfigDir string) (*hypr.Client, *profile.Store, error) {
	base, err := config.EnsureBaseDir(explicitConfigDir)
	if err != nil {
		return nil, nil, err
	}
	client, err := hypr.NewClient()
	if err != nil {
		return nil, nil, err
	}
	store := profile.NewStore(base)
	if err := store.Ensure(); err != nil {
		return nil, nil, err
	}
	return client, store, nil
}

func confirmApplyWithInput(timeoutSec int, input io.Reader, output io.Writer, signals <-chan os.Signal) (bool, error) {
	fmt.Fprintf(output, "Keep this configuration? [y/N] (auto-revert in %ds): ", timeoutSec)
	inputCh := make(chan string, 1)
	errCh := make(chan error, 1)

	go func() {
		reader := bufio.NewReader(input)
		line, err := reader.ReadString('\n')
		if err != nil {
			errCh <- err
			return
		}
		inputCh <- strings.TrimSpace(strings.ToLower(line))
	}()

	select {
	case line := <-inputCh:
		return line == "y" || line == "yes", nil
	case err := <-errCh:
		return false, err
	case <-signals:
		fmt.Fprintln(output)
		return false, nil
	case <-time.After(time.Duration(timeoutSec) * time.Second):
		return false, nil
	}
}

func confirmUnmanagedOverwrite(input io.Reader, output io.Writer, signals <-chan os.Signal, collision *apply.UnmanagedMonitorConfigError) (bool, error) {
	fmt.Fprintf(output, "%s was not generated by hyprmoncfg and will not be replaced automatically.\n", collision.Path)
	fmt.Fprintf(output, "To preserve it, use --monitors-conf %s and include that file instead.\n", collision.AlternativePath)
	fmt.Fprint(output, "Overwrite the existing file once? [y/N]: ")

	inputCh := make(chan string, 1)
	errCh := make(chan error, 1)
	go func() {
		line, err := bufio.NewReader(input).ReadString('\n')
		if err != nil {
			errCh <- err
			return
		}
		inputCh <- strings.TrimSpace(strings.ToLower(line))
	}()

	select {
	case answer := <-inputCh:
		return answer == "y" || answer == "yes", nil
	case err := <-errCh:
		return false, err
	case <-signals:
		fmt.Fprintln(output)
		return false, nil
	}
}
