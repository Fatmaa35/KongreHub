package review

import (
	"errors"
	"reflect"
	"testing"
	"time"
)

func TestWeightedScore(t *testing.T) {
	s := Scores{Originality: 8, Significance: 6, Methodology: 7, Clarity: 9}
	// 8*.30 + 6*.25 + 7*.30 + 9*.15 = 2.4+1.5+2.1+1.35 = 7.35
	if got := s.Weighted(); got != 7.35 {
		t.Fatalf("got %v", got)
	}
	if (Scores{10, 10, 10, 10}).Weighted() != 10 || (Scores{1, 1, 1, 1}).Weighted() != 1 {
		t.Fatal("bounds")
	}
}

func TestWeightsSumToOne(t *testing.T) {
	sum := Weights.Originality + Weights.Significance + Weights.Methodology + Weights.Clarity
	if sum < 0.9999 || sum > 1.0001 {
		t.Fatalf("weights sum %v", sum)
	}
}

func TestScoreValidation(t *testing.T) {
	if err := (Scores{0, 5, 5, 5}).Validate(); !errors.Is(err, ErrInvalidScore) {
		t.Fatal("0 must be rejected")
	}
	if err := (Scores{5, 5, 5, 11}).Validate(); err == nil {
		t.Fatal("11 must be rejected")
	}
	if err := (Scores{1, 10, 5, 5}).Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestDisagreement(t *testing.T) {
	a := Summarize([]float64{8.5, 8.0, 4.9})
	if !a.Disagreement || a.Spread != 3.6 || a.Mean != 7.13 {
		t.Fatalf("%+v", a)
	}
	if !NeedsExtraReviewer(a, false) {
		t.Fatal("should request 4th reviewer")
	}
	if NeedsExtraReviewer(a, true) {
		t.Fatal("4th reviewer only once per round")
	}
	b := Summarize([]float64{7, 6, 4}) // fark tam 3 → eşik aşılmadı
	if b.Disagreement || NeedsExtraReviewer(b, false) {
		t.Fatalf("spread 3 must not trigger: %+v", b)
	}
	if NeedsExtraReviewer(Summarize([]float64{9, 2}), false) {
		t.Fatal("needs 3 reviews before checking")
	}
	if z := Summarize(nil); z.Count != 0 || z.Disagreement {
		t.Fatal("empty")
	}
}

func TestStateMachineHappyPathWithRevision(t *testing.T) {
	steps := []struct {
		ev   Event
		want Status
	}{
		{EvAssign, UnderReview},
		{EvReviewsDone, ReviewsComplete},
		{EvExtraNeeded, UnderReview},
		{EvReviewsDone, ReviewsComplete},
		{EvMajor, RevisionRequested},
		{EvResubmit, UnderReview},
		{EvReviewsDone, ReviewsComplete},
		{EvAccept, Accepted},
	}
	s := Submitted
	for _, st := range steps {
		n, err := Next(s, st.ev)
		if err != nil || n != st.want {
			t.Fatalf("%s --%s--> got %s, %v; want %s", s, st.ev, n, err, st.want)
		}
		s = n
	}
}

func TestStateMachineRejectsIllegal(t *testing.T) {
	bad := []struct {
		from Status
		ev   Event
	}{
		{Submitted, EvAccept},
		{UnderReview, EvAccept},
		{Accepted, EvWithdraw},
		{Rejected, EvResubmit},
		{RevisionRequested, EvAccept},
		{Status("bogus"), EvAssign},
	}
	for _, b := range bad {
		_, err := Next(b.from, b.ev)
		var te ErrTransition
		if !errors.As(err, &te) || te.Error() == "" {
			t.Errorf("%s/%s should fail, got %v", b.from, b.ev, err)
		}
	}
}

func TestDecisionEvent(t *testing.T) {
	for d, ev := range map[string]Event{"accept": EvAccept, "reject": EvReject, "minor_revision": EvMinor, "major_revision": EvMajor} {
		if got, ok := DecisionEvent(d); !ok || got != ev {
			t.Errorf("%s", d)
		}
	}
	if _, ok := DecisionEvent("maybe"); ok {
		t.Error("unknown decision")
	}
}

func TestRevisionReviewers(t *testing.T) {
	prior := []PriorReview{{1, "accept"}, {2, "minor_revision"}, {3, "reject"}}
	if got := RevisionReviewers("minor_revision", prior); !reflect.DeepEqual(got, []int64{2, 3}) {
		t.Fatalf("minor: %v", got)
	}
	if got := RevisionReviewers("major_revision", prior); !reflect.DeepEqual(got, []int64{1, 2, 3}) {
		t.Fatalf("major: %v", got)
	}
	if got := RevisionReviewers("minor_revision", []PriorReview{{5, "accept"}, {6, "accept"}}); !reflect.DeepEqual(got, []int64{5}) {
		t.Fatalf("all-accept: %v", got)
	}
}

func TestDeadlinesAndReminders(t *testing.T) {
	a := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	if !DueAt(a).Equal(a.AddDate(0, 0, 14)) {
		t.Fatal("due")
	}
	day := func(n int) time.Time { return a.Add(time.Duration(n) * 24 * time.Hour) }
	if len(DueReminders(a, day(6))) != 0 {
		t.Fatal("no reminder before day 7")
	}
	if got := DueReminders(a, day(8)); !reflect.DeepEqual(got, []string{"day7"}) {
		t.Fatalf("day8: %v", got)
	}
	if got := DueReminders(a, day(13)); !reflect.DeepEqual(got, []string{"day7", "day12"}) {
		t.Fatalf("day13: %v", got)
	}
	if len(DueReminders(a, day(15))) != 0 || !Expired(a, day(14)) || Expired(a, day(13)) {
		t.Fatal("expiry")
	}
}
