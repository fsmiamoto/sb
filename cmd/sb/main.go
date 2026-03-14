package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"

	lipgloss "charm.land/lipgloss/v2"
	"charm.land/lipgloss/v2/table"
	cli "github.com/urfave/cli/v2"

	"github.com/fsmiamoto/sb/internal/config"
	"github.com/fsmiamoto/sb/internal/sandbox"
)

var version = "dev"

func main() {
	app := &cli.App{
		Name:                 "sb",
		Usage:                "Sandboxing tool for coding agents",
		Version:              version,
		EnableBashCompletion: true,
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:    "config",
				Aliases: []string{"c"},
				Usage:   "Path to config file (default: ~/.config/sb/config.toml)",
			},
		},
		Commands: []*cli.Command{
			createCommand(),
			attachCommand(),
			stopCommand(),
			destroyCommand(),
			listCommand(),
			completionCommand(),
		},
	}

	if err := app.Run(os.Args); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// ---------- helpers ----------

// confirm prompts the user with a y/n question on stderr and reads from stdin.
func confirm(message string) bool {
	fmt.Fprintf(os.Stderr, "%s [y/N] ", message)
	reader := bufio.NewReader(os.Stdin)
	line, _ := reader.ReadString('\n')
	line = strings.TrimSpace(strings.ToLower(line))
	return line == "y" || line == "yes"
}

// warn prints a warning message to stderr.
func warn(message string) {
	fmt.Fprintf(os.Stderr, "Warning: %s\n", message)
}

// exitError prints to stderr and returns a cli.ExitError with code 1.
func exitError(format string, args ...any) error {
	msg := fmt.Sprintf(format, args...)
	return cli.Exit(msg, 1)
}

// loadMergedConfig loads config from the file (or default path) and returns it.
func loadMergedConfig(cCtx *cli.Context) config.Config {
	configPath := cCtx.String("config")
	fileConfig, err := config.LoadConfig(configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: %v\n", err)
	}
	return fileConfig
}

// newManager creates a SandboxManager from a merged config.
func newManager(merged config.MergedConfig) *sandbox.SandboxManager {
	return sandbox.NewSandboxManager(sandbox.SandboxManagerOptions{
		ImageName:           merged.Image,
		ExtraMounts:         merged.ExtraMounts,
		EnvPassthrough:      merged.EnvPassthrough,
		CustomSensitiveDirs: merged.SensitiveDirs,
	})
}

// resolveSandboxByName uses fuzzy matching to find a unique sandbox.
func resolveSandboxByName(ctx context.Context, mgr *sandbox.SandboxManager, query string) (sandbox.SandboxInfo, error) {
	matches, err := mgr.FindSandboxes(ctx, query)
	if err != nil {
		return sandbox.SandboxInfo{}, err
	}

	if len(matches) == 0 {
		return sandbox.SandboxInfo{}, fmt.Errorf("Sandbox '%s' not found", query)
	}

	if len(matches) == 1 {
		return matches[0], nil
	}

	// Multiple matches
	lines := []string{fmt.Sprintf("Multiple sandboxes match '%s':", query)}
	for _, s := range matches {
		lines = append(lines, fmt.Sprintf("  %s  (%s)", s.Name, s.Workspace))
	}
	lines = append(lines, "", "Use the full sandbox name or a more specific query.")
	return sandbox.SandboxInfo{}, fmt.Errorf("%s", strings.Join(lines, "\n"))
}

// formatCreatedAt formats a Docker ISO timestamp for display.
func formatCreatedAt(createdAt string) string {
	if createdAt == "" {
		return ""
	}

	// Docker timestamps have nanosecond precision (9 digits after decimal).
	// Go's time.Parse only handles up to 9 digits, but the trailing Z needs
	// to be handled. Truncate fractional seconds to 6 digits for consistency.
	re := regexp.MustCompile(`\.(\d{6})\d+`)
	timestamp := re.ReplaceAllString(createdAt, ".$1")

	for _, layout := range []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02T15:04:05.999999Z07:00",
		"2006-01-02T15:04:05",
	} {
		if t, err := time.Parse(layout, timestamp); err == nil {
			return t.Format("2006-01-02 15:04")
		}
	}

	// Fallback: return first 16 chars.
	if len(createdAt) >= 16 {
		return createdAt[:16]
	}
	return createdAt
}

// ---------- commands ----------

func createCommand() *cli.Command {
	return &cli.Command{
		Name:    "create",
		Aliases: []string{"c"},
		Usage:   "Create a new sandbox for the current directory",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:    "name",
				Aliases: []string{"n"},
				Usage:   "Explicit sandbox name (auto-generated if not provided)",
			},
			&cli.BoolFlag{
				Name:    "force",
				Aliases: []string{"f"},
				Usage:   "Recreate sandbox if it already exists",
			},
			&cli.BoolFlag{
				Name:    "attach",
				Aliases: []string{"a"},
				Usage:   "Attach to sandbox after creation",
			},
			&cli.StringSliceFlag{
				Name:  "mount",
				Usage: "Additional read-only mount (repeatable)",
			},
			&cli.StringSliceFlag{
				Name:  "env",
				Usage: "Environment variable to pass (VAR or VAR=value, repeatable)",
			},
			&cli.StringFlag{
				Name:  "image",
				Usage: "Override Docker image to use",
			},
		},
		Action: func(cCtx *cli.Context) error {
			fileConfig := loadMergedConfig(cCtx)

			cliMounts := cCtx.StringSlice("mount")
			cliEnvs := cCtx.StringSlice("env")
			cliImage := cCtx.String("image")

			// CLI mounts, envs, and image go through CreateOptions only.
			// The manager handles config-level values internally; passing
			// CLI values through MergeConfig as well would duplicate them.
			merged := config.MergeConfig(fileConfig, config.CLIArgs{})

			mgr := newManager(merged)

			ctx := context.Background()
			sb, err := mgr.Create(ctx, sandbox.CreateOptions{
				Name:        cCtx.String("name"),
				Force:       cCtx.Bool("force"),
				ExtraMounts: cliMounts,
				EnvVars:     cliEnvs,
				Image:       cliImage,
				Confirm:     confirm,
				Warn:        warn,
			})
			if err != nil {
				return exitError("%v", err)
			}

			fmt.Printf("Created sandbox '%s' for workspace '%s'\n", sb.Name, sb.Workspace)

			if cCtx.Bool("attach") {
				attached, err := mgr.Attach(ctx, sb.Name, "")
				if err != nil {
					return exitError("%v", err)
				}
				fmt.Printf("Attached to sandbox '%s'\n", attached.Name)
				exitCode, err := mgr.ExecShell(ctx, attached)
				if err != nil {
					return exitError("%v", err)
				}
				os.Exit(exitCode)
			}

			return nil
		},
	}
}

func attachCommand() *cli.Command {
	return &cli.Command{
		Name:         "attach",
		Aliases:      []string{"a"},
		Usage:        "Attach to a sandbox (auto-starts if stopped)",
		ArgsUsage:    "[name]",
		BashComplete: completeSandboxNames,
		Action: func(cCtx *cli.Context) error {
			fileConfig := loadMergedConfig(cCtx)
			merged := config.MergeConfig(fileConfig, config.CLIArgs{})

			mgr := newManager(merged)

			ctx := context.Background()

			var name string
			if cCtx.NArg() > 0 {
				resolved, err := resolveSandboxByName(ctx, mgr, cCtx.Args().First())
				if err != nil {
					return exitError("%v", err)
				}
				name = resolved.Name
			}

			sb, err := mgr.Attach(ctx, name, "")
			if err != nil {
				return exitError("%v", err)
			}

			fmt.Printf("Attached to sandbox '%s'\n", sb.Name)
			exitCode, err := mgr.ExecShell(ctx, sb)
			if err != nil {
				return exitError("%v", err)
			}
			os.Exit(exitCode)
			return nil
		},
	}
}

func stopCommand() *cli.Command {
	return &cli.Command{
		Name:         "stop",
		Usage:        "Stop a running sandbox",
		ArgsUsage:    "[name]",
		BashComplete: completeSandboxNames,
		Action: func(cCtx *cli.Context) error {
			fileConfig := loadMergedConfig(cCtx)
			merged := config.MergeConfig(fileConfig, config.CLIArgs{})

			mgr := newManager(merged)

			ctx := context.Background()

			var name string
			if cCtx.NArg() > 0 {
				resolved, err := resolveSandboxByName(ctx, mgr, cCtx.Args().First())
				if err != nil {
					return exitError("%v", err)
				}
				name = resolved.Name
			}

			sb, err := mgr.Stop(ctx, name, "")
			if err != nil {
				return exitError("%v", err)
			}
			fmt.Printf("Stopped sandbox '%s'.\n", sb.Name)

			return nil
		},
	}
}

func destroyCommand() *cli.Command {
	return &cli.Command{
		Name:         "destroy",
		Aliases:      []string{"d"},
		Usage:        "Remove a sandbox completely",
		ArgsUsage:    "[name]",
		BashComplete: completeSandboxNames,
		Flags: []cli.Flag{
			&cli.BoolFlag{
				Name:    "force",
				Aliases: []string{"f"},
				Usage:   "Skip confirmation prompt",
			},
		},
		Action: func(cCtx *cli.Context) error {
			fileConfig := loadMergedConfig(cCtx)
			merged := config.MergeConfig(fileConfig, config.CLIArgs{})

			mgr := newManager(merged)

			ctx := context.Background()
			force := cCtx.Bool("force")

			var confirmFunc sandbox.ConfirmFunc
			if !force {
				confirmFunc = confirm
			}

			var name string
			if cCtx.NArg() > 0 {
				resolved, err := resolveSandboxByName(ctx, mgr, cCtx.Args().First())
				if err != nil {
					return exitError("%v", err)
				}
				name = resolved.Name
			}

			sb, err := mgr.Destroy(ctx, sandbox.DestroyOptions{
				Name:    name,
				Force:   force,
				Confirm: confirmFunc,
			})
			if err != nil {
				return exitError("%v", err)
			}
			fmt.Printf("Destroyed sandbox '%s'.\n", sb.Name)

			return nil
		},
	}
}

func listCommand() *cli.Command {
	return &cli.Command{
		Name:  "list",
		Usage: "List all sandboxes with status",
		Action: func(cCtx *cli.Context) error {
			fileConfig := loadMergedConfig(cCtx)
			merged := config.MergeConfig(fileConfig, config.CLIArgs{})

			mgr := newManager(merged)

			ctx := context.Background()
			sandboxes, err := mgr.List(ctx)
			if err != nil {
				return exitError("%v", err)
			}

			if len(sandboxes) == 0 {
				fmt.Println("No sandboxes found. Use 'sb create' to create one.")
				return nil
			}

			printSandboxTable(ctx, mgr, sandboxes)
			return nil
		},
	}
}

// Table style constants — match the Python Rich table colors.
var (
	greenStyle  = lipgloss.NewStyle().Foreground(lipgloss.Green)
	yellowStyle = lipgloss.NewStyle().Foreground(lipgloss.Yellow)
	dimStyle    = lipgloss.NewStyle().Foreground(lipgloss.BrightBlack)
	headerStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.White)
	borderStyle = lipgloss.NewStyle().Foreground(lipgloss.BrightBlack)
)

const (
	colName      = 0
	colWorkspace = 1
	colStatus    = 2
	colCreated   = 3
)

// statusText returns the display-ready status and its style.
func statusText(raw string) (string, lipgloss.Style) {
	switch raw {
	case "running":
		return "running", greenStyle
	case "exited", "stopped", "created":
		return "stopped", yellowStyle
	default:
		return "unknown", dimStyle
	}
}

// printSandboxTable outputs a lipgloss-styled table of sandboxes.
func printSandboxTable(ctx context.Context, mgr *sandbox.SandboxManager, sandboxes []sandbox.SandboxInfo) {
	// Build rows and collect per-row status styles.
	type rowMeta struct {
		statusStyle lipgloss.Style
	}
	metas := make([]rowMeta, 0, len(sandboxes))

	t := table.New().
		Headers("NAME", "WORKSPACE", "STATUS", "CREATED").
		Border(lipgloss.RoundedBorder()).
		BorderStyle(borderStyle)

	for _, sb := range sandboxes {
		raw, err := mgr.GetContainerStatus(ctx, sb)
		if err != nil {
			raw = "unknown"
		}
		display, sty := statusText(raw)
		metas = append(metas, rowMeta{statusStyle: sty})
		t.Row(sb.Name, sb.Workspace, display, formatCreatedAt(sb.CreatedAt))
	}

	t.StyleFunc(func(row, col int) lipgloss.Style {
		base := lipgloss.NewStyle().PaddingLeft(1).PaddingRight(1)
		if row == table.HeaderRow {
			return headerStyle.PaddingLeft(1).PaddingRight(1)
		}
		switch col {
		case colName:
			return base.Foreground(lipgloss.Cyan)
		case colWorkspace:
			return base
		case colStatus:
			if row >= 0 && row < len(metas) {
				return base.Inherit(metas[row].statusStyle)
			}
			return base
		case colCreated:
			return base.Foreground(lipgloss.BrightBlack)
		}
		return base
	})

	lipgloss.Println(t)
}
