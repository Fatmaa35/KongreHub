// Package keywords normalizes keyword lists and scores topical similarity.
//
// Skor, hakem uzmanlığı ile bildiri anahtar kelimeleri arasındaki örtüşmeyi
// ölçer. Basit Jaccard yerine "bildiri kapsaması" kullanılır: bildirinin
// anahtar kelimelerinin ne kadarını hakem karşılıyor? Böylece geniş uzmanlık
// listesi olan hakem cezalandırılmaz.
package keywords

import (
	"sort"
	"strings"
	"unicode"
)

// Normalize lower-cases, trims, collapses inner whitespace and de-duplicates.
func Normalize(in []string) []string {
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, k := range in {
		k = strings.Join(strings.FieldsFunc(strings.ToLower(k), func(r rune) bool {
			return unicode.IsSpace(r) || r == '_'
		}), " ")
		if k == "" {
			continue
		}
		if _, ok := seen[k]; ok {
			continue
		}
		seen[k] = struct{}{}
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Set is a normalized keyword set for O(1) membership tests.
type Set map[string]struct{}

func NewSet(kw []string) Set {
	s := make(Set, len(kw))
	for _, k := range Normalize(kw) {
		s[k] = struct{}{}
	}
	return s
}

// Coverage returns |paper ∩ expert| / |paper| in [0,1].
func Coverage(paper, expert Set) float64 {
	if len(paper) == 0 {
		return 0
	}
	hit := 0
	for k := range paper {
		if _, ok := expert[k]; ok {
			hit++
		}
	}
	return float64(hit) / float64(len(paper))
}

// Jaccard returns |a ∩ b| / |a ∪ b| in [0,1]; used for paper↔paper similarity
// when grouping sessions (simetrik ölçü gerekir).
func Jaccard(a, b Set) float64 {
	if len(a) == 0 && len(b) == 0 {
		return 0
	}
	inter := 0
	for k := range a {
		if _, ok := b[k]; ok {
			inter++
		}
	}
	union := len(a) + len(b) - inter
	return float64(inter) / float64(union)
}
