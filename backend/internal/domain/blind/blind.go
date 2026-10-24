// Package blind implements double-blind identity rules: who may see whose
// identity, pseudonymous reviewer labels and a leak scanner used by tests and
// `make verify`.
package blind

import (
	"bytes"
	"sort"
	"strings"
)

// Role names (users.role).
const (
	Author    = "author"
	Reviewer  = "reviewer"
	Editor    = "editor"
	Registrar = "registrar"
	Attendee  = "attendee"
	Admin     = "admin"
)

// CanSeeAuthors: hakem yazar kimliğini göremez; editör ve yönetim görür.
// Yazar kendi bildirisinin yazarlarını görür (çağıran sahipliği ayrıca kontrol eder).
func CanSeeAuthors(role string) bool {
	return role == Editor || role == Admin || role == Author
}

// CanSeeReviewers: yalnızca editör ve yönetim hakem kimliğini görür.
func CanSeeReviewers(role string) bool { return role == Editor || role == Admin }

// ReviewerLabels assigns stable pseudonyms ("Hakem A", "Hakem B" …) to the
// assignment IDs of one submission, ordered by assignment id so labels never
// change between requests and reveal nothing about the reviewer.
func ReviewerLabels(assignmentIDs []int64) map[int64]string {
	ids := append([]int64(nil), assignmentIDs...)
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	out := make(map[int64]string, len(ids))
	for i, id := range ids {
		out[id] = "Hakem " + label(i)
	}
	return out
}

func label(i int) string {
	s := ""
	for {
		s = string(rune('A'+i%26)) + s
		i = i/26 - 1
		if i < 0 {
			return s
		}
	}
}

// Finding is one leaked identity token.
type Finding struct {
	Token string
	Kind  string
}

// Scan reports which forbidden tokens occur in body (case-insensitive).
// Çok kısa tokenlar (<4) yanlış alarm üretmemek için yok sayılır.
func Scan(body []byte, forbidden map[string]string) []Finding {
	low := bytes.ToLower(body)
	var out []Finding
	keys := make([]string, 0, len(forbidden))
	for k := range forbidden {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, tok := range keys {
		t := strings.ToLower(strings.TrimSpace(tok))
		if len(t) < 4 {
			continue
		}
		if bytes.Contains(low, []byte(t)) {
			out = append(out, Finding{Token: tok, Kind: forbidden[tok]})
		}
	}
	return out
}
