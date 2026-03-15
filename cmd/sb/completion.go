package main

import (
	"context"
	"fmt"
	"os"

	cli "github.com/urfave/cli/v2"

	"github.com/fsmiamoto/sb/internal/sandbox"
)

// completeSandboxNames provides dynamic completion of sandbox names
// for commands that accept a sandbox name argument (attach, stop, destroy).
func completeSandboxNames(cCtx *cli.Context) {
	if cCtx.NArg() > 0 {
		return // already have a positional arg
	}

	mgr := sandbox.NewSandboxManager(sandbox.SandboxManagerOptions{})
	ctx := context.Background()
	sandboxes, err := mgr.List(ctx)
	if err != nil {
		return // silently fail — don't break shell completion
	}

	for _, sb := range sandboxes {
		_, _ = fmt.Fprintln(cCtx.App.Writer, sb.Name)
	}
}

func completionCommand() *cli.Command {
	return &cli.Command{
		Name:  "completion",
		Usage: "Generate shell completion scripts",
		Subcommands: []*cli.Command{
			{
				Name:  "bash",
				Usage: "Generate bash completion script",
				Action: func(cCtx *cli.Context) error {
					_, _ = fmt.Fprint(os.Stdout, bashCompletionScript)
					return nil
				},
			},
			{
				Name:  "zsh",
				Usage: "Generate zsh completion script",
				Action: func(cCtx *cli.Context) error {
					_, _ = fmt.Fprint(os.Stdout, zshCompletionScript)
					return nil
				},
			},
			{
				Name:  "fish",
				Usage: "Generate fish completion script",
				Action: func(cCtx *cli.Context) error {
					script, err := cCtx.App.ToFishCompletion()
					if err != nil {
						return cli.Exit(fmt.Sprintf("failed to generate fish completion: %v", err), 1)
					}
					_, _ = fmt.Fprint(os.Stdout, script)
					return nil
				},
			},
		},
		Action: func(cCtx *cli.Context) error {
			return cli.Exit(`Generate shell completion scripts.

Usage:
  sb completion bash    # Bash completion
  sb completion zsh     # Zsh completion
  sb completion fish    # Fish completion

Setup:
  # Bash (add to ~/.bashrc):
  eval "$(sb completion bash)"

  # Zsh (add to ~/.zshrc):
  eval "$(sb completion zsh)"

  # Fish (run once):
  sb completion fish > ~/.config/fish/completions/sb.fish`, 0)
		},
	}
}

const bashCompletionScript = `# sb bash completion
# Add to ~/.bashrc: eval "$(sb completion bash)"

_sb_completions() {
    local cur prev words cword
    _init_completion || return

    # Use urfave/cli's built-in completion mechanism
    local completions
    completions=$(COMP_LINE="${COMP_LINE}" COMP_POINT="${COMP_POINT}" "${words[0]}" "${words[@]:1}" --generate-bash-completion 2>/dev/null)
    COMPREPLY=($(compgen -W "${completions}" -- "${cur}"))
    return 0
}

complete -o default -F _sb_completions sb
`

const zshCompletionScript = `# sb zsh completion
# Add to ~/.zshrc: eval "$(sb completion zsh)"

_sb() {
    local -a completions
    local -a completions_with_descriptions
    local -a response

    # Use urfave/cli's built-in completion mechanism
    response=("${(@f)$(env COMP_LINE="${words[*]}" COMP_POINT=$CURSOR ${words[0]} "${(@)words[1,$CURRENT]}" --generate-bash-completion 2>/dev/null)}")

    for key in "${response[@]}"; do
        if [[ "$key" == *":"* ]]; then
            completions_with_descriptions+=("$key")
        else
            completions+=("$key")
        fi
    done

    if [ -n "$completions_with_descriptions" ]; then
        _describe -V unsorted completions_with_descriptions -U
    fi

    if [ -n "$completions" ]; then
        compadd -U -V unsorted -a completions
    fi
}

if [ "$funcstack[1]" = "_sb" ]; then
    _sb "$@"
else
    compdef _sb sb
fi
`
