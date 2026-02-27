#!/bin/bash

# Ralph - runs Claude Code or Codex in a loop until all tasks complete
# Usage: ralph [PROMPT_FILE] [--agent claude|codex] [--max-iterations N]

set -e

# Defaults
PROMPT_FILE="PROMPT.md"
AGENT="claude"
MAX_ITERATIONS=0  # 0 = unlimited
OUTPUT_FILE=$(mktemp)

cleanup() {
    rm -f "$OUTPUT_FILE"
}
trap cleanup EXIT

# Parse arguments
while [[ $# -gt 0 ]]; do
    case "$1" in
        -a|--agent)
            AGENT="$2"
            shift 2
            ;;
        -m|--max-iterations)
            MAX_ITERATIONS="$2"
            shift 2
            ;;
        -h|--help)
            echo "Usage: ralph [PROMPT_FILE] [OPTIONS]"
            echo ""
            echo "Arguments:"
            echo "  PROMPT_FILE              Path to prompt file (default: PROMPT.md)"
            echo ""
            echo "Options:"
            echo "  -a, --agent NAME         Agent to use: claude or codex (default: claude)"
            echo "  -m, --max-iterations N   Maximum iterations, 0 for unlimited (default: 0)"
            echo "  -h, --help               Show this help message"
            echo ""
            echo "Ctrl+C interrupts the current agent run and prompts for next action."
            exit 0
            ;;
        -*)
            echo "Error: Unknown option $1"
            exit 1
            ;;
        *)
            PROMPT_FILE="$1"
            shift
            ;;
    esac
done

if [[ ! -f "$PROMPT_FILE" ]]; then
    echo "Error: $PROMPT_FILE not found"
    exit 1
fi

PROMPT=$(cat "$PROMPT_FILE")

iteration=0

echo "Ralph starting with $AGENT"
[[ $MAX_ITERATIONS -gt 0 ]] && echo "Max iterations: $MAX_ITERATIONS"
echo "Prompt file: $PROMPT_FILE"
echo "Ctrl+C to interrupt agent"
echo "=============================================="

while true; do
    iteration=$((iteration + 1))

    if [[ $MAX_ITERATIONS -gt 0 && $iteration -gt $MAX_ITERATIONS ]]; then
        echo ""
        echo "=============================================="
        echo "Warning: Reached maximum iterations ($MAX_ITERATIONS) without completion"
        exit 1
    fi

    echo ""
    echo ">>> Iteration $iteration"
    echo "----------------------------------------------"

    # Clear output file
    > "$OUTPUT_FILE"

    # Run agent, tee output to file, allow Ctrl+C to interrupt
    set +e
    case "$AGENT" in
        claude)
            claude -p "$PROMPT" --allowedTools "Bash(git *)" "Bash(npm *)" "Bash(pnpm *)" "Bash(yarn *)" "Bash(npx *)" "Bash(node *)" "Bash(make *)" "Bash(cargo *)" "Read" "Write" "Edit" "Glob" "Grep" "Task" "Skill" 2>&1 | tee "$OUTPUT_FILE"
            ;;
        codex)
            codex -q "$PROMPT" 2>&1 | tee "$OUTPUT_FILE"
            ;;
        *)
            echo "Error: Unknown agent '$AGENT'. Use 'claude' or 'codex'"
            exit 1
            ;;
    esac
    exit_code=${PIPESTATUS[0]}
    set -e

    # Handle interrupted agent
    if [[ $exit_code -eq 130 ]]; then
        echo ""
        echo "[Agent interrupted]"
        while true; do
            read -p "Continue to next iteration? [y/n]: " choice
            case "$choice" in
                y|Y|yes|Yes)
                    break
                    ;;
                n|N|no|No)
                    echo "Ralph stopped by user after $iteration iterations."
                    exit 0
                    ;;
                *)
                    echo "Please enter y or n"
                    ;;
            esac
        done
        continue
    fi

    # Check for completion marker
    if grep -q '<promise>COMPLETE</promise>' "$OUTPUT_FILE"; then
        echo ""
        echo "=============================================="
        echo "Ralph complete! Finished after $iteration iterations."
        exit 0
    fi

    sleep 2
done
