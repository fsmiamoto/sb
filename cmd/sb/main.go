package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
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

// loadMergedConfig loads config from the file (or default path) and flattens
// it into a MergedConfig ready for constructing a SandboxManager.
func loadMergedConfig(cCtx *cli.Context) config.MergedConfig {
	configPath := cCtx.String("config")
	fileConfig, err := config.LoadConfig(configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: %v\n", err)
	}
	return config.MergeConfig(fileConfig)
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

// resolveNameArg extracts the sandbox name from the first positional argument
// using fuzzy matching. Returns empty string if no argument is provided.
func resolveNameArg(mgr *sandbox.SandboxManager, cCtx *cli.Context) (string, error) {
	if cCtx.NArg() == 0 {
		return "", nil
	}
	resolved, err := resolveSandboxByName(cCtx.Context, mgr, cCtx.Args().First())
	if err != nil {
		return "", err
	}
	return resolved.Name, nil
}

// resolveSandboxByName uses fuzzy matching to find a unique sandbox.
func resolveSandboxByName(ctx context.Context, mgr *sandbox.SandboxManager, query string) (sandbox.SandboxInfo, error) {
	matches, err := mgr.FindSandboxes(ctx, query)
	if err != nil {
		return sandbox.SandboxInfo{}, err
	}

	if len(matches) == 0 {
		return sandbox.SandboxInfo{}, fmt.Errorf("sandbox '%s' not found", query)
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
	timestamp := nanosecondTruncateRe.ReplaceAllString(createdAt, ".$1")

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
			merged := loadMergedConfig(cCtx)

			cliMounts := cCtx.StringSlice("mount")
			cliEnvs := cCtx.StringSlice("env")
			cliImage := cCtx.String("image")

			mgr := newManager(merged)
			defer func() { _ = mgr.Close() }()

			ctx := cCtx.Context
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
			merged := loadMergedConfig(cCtx)
			mgr := newManager(merged)
			defer func() { _ = mgr.Close() }()

			name, err := resolveNameArg(mgr, cCtx)
			if err != nil {
				return exitError("%v", err)
			}

			ctx := cCtx.Context
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
		Aliases:      []string{"s"},
		Usage:        "Stop a running sandbox",
		ArgsUsage:    "[name]",
		BashComplete: completeSandboxNames,
		Action: func(cCtx *cli.Context) error {
			merged := loadMergedConfig(cCtx)
			mgr := newManager(merged)
			defer func() { _ = mgr.Close() }()

			name, err := resolveNameArg(mgr, cCtx)
			if err != nil {
				return exitError("%v", err)
			}

			sb, err := mgr.Stop(cCtx.Context, name, "")
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
			merged := loadMergedConfig(cCtx)
			mgr := newManager(merged)
			defer func() { _ = mgr.Close() }()

			force := cCtx.Bool("force")

			var confirmFunc sandbox.ConfirmFunc
			if !force {
				confirmFunc = confirm
			}

			name, err := resolveNameArg(mgr, cCtx)
			if err != nil {
				return exitError("%v", err)
			}

			sb, err := mgr.Destroy(cCtx.Context, sandbox.DestroyOptions{
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
		Name:    "list",
		Aliases: []string{"ls"},
		Usage:   "List all sandboxes with status",
		Flags: []cli.Flag{
			&cli.BoolFlag{
				Name:  "json",
				Usage: "Output as JSON",
			},
		},
		Action: func(cCtx *cli.Context) error {
			merged := loadMergedConfig(cCtx)
			mgr := newManager(merged)
			defer func() { _ = mgr.Close() }()

			sandboxes, err := mgr.List(cCtx.Context)
			if err != nil {
				return exitError("%v", err)
			}

			if cCtx.Bool("json") {
				return printSandboxJSON(os.Stdout, sandboxes)
			}

			if len(sandboxes) == 0 {
				fmt.Println("No sandboxes found. Use 'sb create' to create one.")
				return nil
			}

			printSandboxTable(os.Stdout, sandboxes)
			return nil
		},
	}
}

// Table style constants — match the Python Rich table colors.
var (
	greenStyle  = lipgloss.NewStyle().Foreground(lipgloss.Green)
	yellowStyle = lipgloss.NewStyle().Foreground(lipgloss.Yellow)
	dimStyle    = lipgloss.NewStyle().Foreground(lipgloss.BrightBlack)
	borderStyle = lipgloss.NewStyle().Foreground(lipgloss.BrightBlack)

	// Cell styles used by StyleFunc — computed once instead of per-cell.
	cellBase    = lipgloss.NewStyle().PaddingLeft(1).PaddingRight(1)
	headerCell  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.White).PaddingLeft(1).PaddingRight(1)
	nameCell    = cellBase.Foreground(lipgloss.Cyan)
	createdCell = cellBase.Foreground(lipgloss.BrightBlack)

	// nanosecondTruncateRe truncates fractional seconds beyond 6 digits
	// in Docker timestamps so Go's time.Parse can handle them.
	nanosecondTruncateRe = regexp.MustCompile(`\.(\d{6})\d+`)
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

// printSandboxTable outputs a lipgloss-styled table of sandboxes to w.
func printSandboxTable(w io.Writer, sandboxes []sandbox.SandboxInfo) {
	// Build rows and collect per-row status styles.
	statusStyles := make([]lipgloss.Style, 0, len(sandboxes))

	t := table.New().
		Headers("NAME", "WORKSPACE", "STATUS", "CREATED").
		Border(lipgloss.RoundedBorder()).
		BorderStyle(borderStyle)

	for _, sb := range sandboxes {
		display, sty := statusText(sb.Status)
		statusStyles = append(statusStyles, sty)
		t.Row(sb.Name, sb.Workspace, display, formatCreatedAt(sb.CreatedAt))
	}

	t.StyleFunc(func(row, col int) lipgloss.Style {
		if row == table.HeaderRow {
			return headerCell
		}
		switch col {
		case colName:
			return nameCell
		case colStatus:
			if row >= 0 && row < len(statusStyles) {
				return cellBase.Inherit(statusStyles[row])
			}
			return cellBase
		case colCreated:
			return createdCell
		}
		return cellBase
	})

	_, _ = fmt.Fprintln(w, t)
}

// sandboxJSON is the JSON representation of a sandbox for --json output.
type sandboxJSON struct {
	Name      string `json:"name"`
	Workspace string `json:"workspace"`
	Status    string `json:"status"`
	CreatedAt string `json:"created_at,omitempty"`
}

// printSandboxJSON outputs sandboxes as a JSON array.
func printSandboxJSON(w io.Writer, sandboxes []sandbox.SandboxInfo) error {
	items := make([]sandboxJSON, 0, len(sandboxes))
	for _, sb := range sandboxes {
		display, _ := statusText(sb.Status)
		items = append(items, sandboxJSON{
			Name:      sb.Name,
			Workspace: sb.Workspace,
			Status:    display,
			CreatedAt: sb.CreatedAt,
		})
	}

	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(items); err != nil {
		return exitError("encode JSON: %v", err)
	}
	return nil
}
