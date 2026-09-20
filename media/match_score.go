package media

import (
	"math"
	"regexp"
	"sort"
	"strings"
)

func bestCandidate(parsed ParsedName, candidates []Candidate) (Candidate, float64, bool) {
	type scored struct {
		candidate Candidate
		score     float64
	}
	items := make([]scored, 0, len(candidates))
	for _, candidate := range candidates {
		score := titleScore(parsed.Title, candidate.Title)
		if original := titleScore(parsed.Title, candidate.OriginalTitle); original > score {
			score = original
		}
		for _, alias := range candidate.Aliases {
			if value := titleScore(parsed.Title, alias); value > score {
				score = value
			}
		}
		if parsed.Year != nil && candidate.Year != nil {
			difference := abs(*parsed.Year - *candidate.Year)
			if difference == 0 {
				score += .2
			} else if difference == 1 {
				score += .1
			} else {
				score -= math.Min(.2, float64(difference)*.05)
			}
		}
		score += math.Min(.05, candidate.Popularity/1000)
		items = append(items, scored{candidate, score})
	}
	sort.SliceStable(items, func(i, j int) bool { return items[i].score > items[j].score })
	if len(items) == 0 {
		return Candidate{}, 0, false
	}
	return items[0].candidate, items[0].score, true
}

var nonTitle = regexp.MustCompile(`[^\pL\pN]+`)

func normalizedTitle(value string) string {
	return nonTitle.ReplaceAllString(strings.ToLower(value), "")
}
func titleScore(left, right string) float64 {
	a, b := normalizedTitle(left), normalizedTitle(right)
	if a == "" || b == "" {
		return 0
	}
	if a == b {
		return .8
	}
	if strings.Contains(a, b) || strings.Contains(b, a) {
		return .62
	}
	return 0
}
func contains(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}
func abs(value int) int {
	if value < 0 {
		return -value
	}
	return value
}
