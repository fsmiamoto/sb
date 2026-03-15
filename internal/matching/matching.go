package matching

import (
	"cmp"
	"regexp"
	"slices"
	"strings"
)

// NamedSandbox is the minimal interface required by the matching helpers.
// The sandbox package satisfies this with its SandboxInfo type.
type NamedSandbox interface {
	GetName() string
}

var sandboxNamePattern = regexp.MustCompile(`^sb-(.+)-[a-f0-9]{8}$`)

type scoredSandbox[T NamedSandbox] struct {
	score   int
	sandbox T
}

func extractDirname(sandboxName string) string {
	match := sandboxNamePattern.FindStringSubmatch(sandboxName)
	if len(match) == 2 {
		return match[1]
	}

	return sandboxName
}

// ScoreMatch returns the match score for a sandbox query.
//
// Lower scores are better:
//   - 0: exact match
//   - 10: prefix match
//   - 20: dirname exact match
//   - 30: dirname prefix match
//   - 40: dirname contains match
//   - 50: substring match
func ScoreMatch(query string, sandbox NamedSandbox) (int, bool) {
	name := sandbox.GetName()
	queryLower := strings.ToLower(query)
	nameLower := strings.ToLower(name)

	if queryLower == nameLower {
		return 0, true
	}

	if strings.HasPrefix(nameLower, queryLower) {
		return 10, true
	}

	dirnameLower := strings.ToLower(extractDirname(name))
	if queryLower == dirnameLower {
		return 20, true
	}

	if strings.HasPrefix(dirnameLower, queryLower) {
		return 30, true
	}

	if strings.Contains(dirnameLower, queryLower) {
		return 40, true
	}

	if strings.Contains(nameLower, queryLower) {
		return 50, true
	}

	return 0, false
}

// FindMatchingSandboxes returns matching sandboxes ordered by best match first.
//
// If an exact match exists, only that sandbox is returned.
func FindMatchingSandboxes[T NamedSandbox](query string, sandboxes []T) []T {
	scored := make([]scoredSandbox[T], 0, len(sandboxes))
	for _, sandbox := range sandboxes {
		score, ok := ScoreMatch(query, sandbox)
		if !ok {
			continue
		}

		scored = append(scored, scoredSandbox[T]{
			score:   score,
			sandbox: sandbox,
		})
	}

	if len(scored) == 0 {
		return []T{}
	}

	slices.SortStableFunc(scored, func(a, b scoredSandbox[T]) int {
		return cmp.Compare(a.score, b.score)
	})

	if scored[0].score == 0 {
		return []T{scored[0].sandbox}
	}

	matches := make([]T, 0, len(scored))
	for _, entry := range scored {
		matches = append(matches, entry.sandbox)
	}

	return matches
}
