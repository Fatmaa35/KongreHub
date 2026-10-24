package seed

import (
	"testing"
	"time"
)

var ref = time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)

func TestDeterministic(t *testing.T) {
	a, b := Generate(42, ref), Generate(42, ref)
	if a.Hash() != b.Hash() {
		t.Fatal("same seed must produce identical dataset")
	}
	if Generate(43, ref).Hash() == a.Hash() {
		t.Fatal("different seed should differ")
	}
}

func TestShape(t *testing.T) {
	d := Generate(42, ref)
	if len(d.Submissions) != NumSubmissions || len(d.Reviewers) != NumReviewers {
		t.Fatalf("%d submissions %d reviewers", len(d.Submissions), len(d.Reviewers))
	}
	emails := map[string]bool{}
	for _, u := range d.Users {
		if emails[u.Email] {
			t.Fatalf("duplicate email %s", u.Email)
		}
		emails[u.Email] = true
	}
	for _, e := range []string{DemoAdmin, DemoEditor, DemoRegistrar, DemoAuthor, DemoReviewer, DemoAttendee} {
		if !emails[e] {
			t.Fatalf("missing demo account %s", e)
		}
	}
	late, overdue := 0, 0
	for _, r := range d.Reviewers {
		if r.Late {
			late++
		}
	}
	for _, a := range d.Assignments {
		if a.Status == "assigned" && ref.Sub(a.AssignedAt) > 14*24*time.Hour {
			overdue++
		}
	}
	if late == 0 || overdue == 0 || len(d.Coauthorships) == 0 || len(d.Registrations) == 0 {
		t.Fatalf("late=%d overdue=%d coauth=%d regs=%d", late, overdue, len(d.Coauthorships), len(d.Registrations))
	}
	t.Logf("users=%d assignments=%d reviews=%d registrations=%d overdue=%d hash=%s", len(d.Users), len(d.Assignments), len(d.Reviews), len(d.Registrations), overdue, d.Hash()[:12])
}
