package cli

import (
	"strconv"

	"github.com/andreswebs/feedwatch/internal/core"
)

// suggestMaxDistance bounds how different a candidate may be from the input
// before it stops being a plausible typo. Two edits catches the common
// transposition and single insert/delete/substitute mistakes without offering
// absurd matches for unrelated words.
const suggestMaxDistance = 2

// unknownFieldMessage builds the --fields usage error for an unrecognized name,
// appending a did-you-mean suggestion when a valid field is a close match.
func unknownFieldMessage(name string) string {
	msg := "--fields: unknown field " + strconv.Quote(name)
	if s, ok := nearestField(name); ok {
		msg += "; did you mean " + strconv.Quote(s) + "?"
	}
	return msg
}

// nearestField returns the valid item field closest to name by Levenshtein
// distance, when that distance is within suggestMaxDistance and strictly less
// than len(name) (so a short garbage name does not match an unrelated field).
// Candidates are iterated in sorted order and ties break by the shortest
// distance, so the suggestion is stable across calls.
func nearestField(name string) (string, bool) {
	best := ""
	bestDist := suggestMaxDistance + 1
	for _, cand := range core.ItemFieldNames() {
		d := levenshtein(name, cand)
		if d < bestDist {
			best, bestDist = cand, d
		}
	}
	if best == "" || bestDist > suggestMaxDistance || bestDist >= len(name) {
		return "", false
	}
	return best, true
}

// levenshtein returns the edit distance between a and b: the minimum number of
// single-character insertions, deletions, or substitutions to turn one into the
// other. It uses a single rolling row, O(len(b)) space.
func levenshtein(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	prev := make([]int, len(rb)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ra); i++ {
		diag := prev[0]
		prev[0] = i
		for j := 1; j <= len(rb); j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			cur := min3(prev[j]+1, prev[j-1]+1, diag+cost)
			diag = prev[j]
			prev[j] = cur
		}
	}
	return prev[len(rb)]
}

func min3(a, b, c int) int {
	m := a
	if b < m {
		m = b
	}
	if c < m {
		m = c
	}
	return m
}
