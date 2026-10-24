// Package review holds review scoring, disagreement detection, the submission
// decision state machine and deadline rules. Saf fonksiyonlardır; veritabanı
// katmanı bu kuralları çağırır.
package review

import (
	"errors"
	"fmt"
	"math"
	"time"
)

// Scores is one reviewer's form (1..10 per criterion).
type Scores struct {
	Originality  int `json:"originality"`
	Significance int `json:"significance"`
	Methodology  int `json:"methodology"`
	Clarity      int `json:"clarity"`
}

// Weights per criterion; toplamı 1 olmalıdır.
var Weights = struct{ Originality, Significance, Methodology, Clarity float64 }{0.30, 0.25, 0.30, 0.15}

// DisagreementThreshold: ağırlıklı skorlar arası fark bunu aşarsa 4. hakem.
const DisagreementThreshold = 3.0

// RequiredReviewers per round.
const RequiredReviewers = 3

// ResponseDays: bu sürede yanıt vermeyen hakemin ataması düşer.
const ResponseDays = 14

// ReminderDays: hatırlatma günleri (atamadan itibaren).
var ReminderDays = map[string]int{"day7": 7, "day12": 12}

var ErrInvalidScore = errors.New("her ölçüt 1 ile 10 arasında olmalı")

// Validate checks score ranges.
func (s Scores) Validate() error {
	for _, v := range []int{s.Originality, s.Significance, s.Methodology, s.Clarity} {
		if v < 1 || v > 10 {
			return ErrInvalidScore
		}
	}
	return nil
}

// Weighted returns the weighted score rounded to 2 decimals.
func (s Scores) Weighted() float64 {
	w := float64(s.Originality)*Weights.Originality +
		float64(s.Significance)*Weights.Significance +
		float64(s.Methodology)*Weights.Methodology +
		float64(s.Clarity)*Weights.Clarity
	return math.Round(w*100) / 100
}

// Aggregate summarises a set of weighted review scores.
type Aggregate struct {
	Count        int     `json:"count"`
	Mean         float64 `json:"mean"`
	Min          float64 `json:"min"`
	Max          float64 `json:"max"`
	Spread       float64 `json:"spread"`
	Disagreement bool    `json:"disagreement"`
}

// Summarize computes mean/spread and flags disagreement (spread > threshold).
func Summarize(weighted []float64) Aggregate {
	a := Aggregate{Count: len(weighted)}
	if len(weighted) == 0 {
		return a
	}
	a.Min, a.Max = math.Inf(1), math.Inf(-1)
	sum := 0.0
	for _, w := range weighted {
		sum += w
		a.Min = math.Min(a.Min, w)
		a.Max = math.Max(a.Max, w)
	}
	a.Mean = math.Round(sum/float64(len(weighted))*100) / 100
	a.Spread = math.Round((a.Max-a.Min)*100) / 100
	a.Disagreement = a.Spread > DisagreementThreshold
	return a
}

// NeedsExtraReviewer: 3 değerlendirme tamamlanmış, uyuşmazlık var ve bu turda
// daha önce ek hakem istenmemişse true.
func NeedsExtraReviewer(agg Aggregate, extraAlreadyRequested bool) bool {
	return agg.Count >= RequiredReviewers && agg.Disagreement && !extraAlreadyRequested
}

// ---- durum makinesi --------------------------------------------------------

type Status string

const (
	Submitted         Status = "submitted"
	UnderReview       Status = "under_review"
	ReviewsComplete   Status = "reviews_complete"
	RevisionRequested Status = "revision_requested"
	Accepted          Status = "accepted"
	Rejected          Status = "rejected"
	Withdrawn         Status = "withdrawn"
)

type Event string

const (
	EvAssign      Event = "assign"       // hakemler atandı
	EvReviewsDone Event = "reviews_done" // gerekli tüm değerlendirmeler geldi
	EvExtraNeeded Event = "extra_needed" // uyuşmazlık → 4. hakem; tekrar incelemede
	EvAccept      Event = "accept"
	EvReject      Event = "reject"
	EvMinor       Event = "minor_revision"
	EvMajor       Event = "major_revision"
	EvResubmit    Event = "resubmit" // yazar revizyon yükledi
	EvWithdraw    Event = "withdraw"
)

var transitions = map[Status]map[Event]Status{
	Submitted:         {EvAssign: UnderReview, EvWithdraw: Withdrawn, EvReject: Rejected},
	UnderReview:       {EvReviewsDone: ReviewsComplete, EvWithdraw: Withdrawn},
	ReviewsComplete:   {EvExtraNeeded: UnderReview, EvAccept: Accepted, EvReject: Rejected, EvMinor: RevisionRequested, EvMajor: RevisionRequested, EvWithdraw: Withdrawn},
	RevisionRequested: {EvResubmit: UnderReview, EvWithdraw: Withdrawn},
	Accepted:          {},
	Rejected:          {},
	Withdrawn:         {},
}

// ErrTransition is returned for illegal transitions.
type ErrTransition struct {
	From  Status
	Event Event
}

func (e ErrTransition) Error() string {
	return fmt.Sprintf("'%s' durumundaki bildiri için '%s' işlemi geçersiz", e.From, e.Event)
}

// Next returns the target status or an ErrTransition.
func Next(from Status, ev Event) (Status, error) {
	m, ok := transitions[from]
	if !ok {
		return "", ErrTransition{from, ev}
	}
	to, ok := m[ev]
	if !ok {
		return "", ErrTransition{from, ev}
	}
	return to, nil
}

// MaxRounds limits revision rounds.
const MaxRounds = 3

// DecisionEvent maps an editor decision string to an event.
func DecisionEvent(decision string) (Event, bool) {
	switch decision {
	case "accept":
		return EvAccept, true
	case "reject":
		return EvReject, true
	case "minor_revision":
		return EvMinor, true
	case "major_revision":
		return EvMajor, true
	}
	return "", false
}

// RevisionReviewers decides who re-reviews the revised version:
// küçük düzeltmede yalnızca düzeltme/ret öneren hakemler; büyük düzeltmede
// önceki turu tamamlayan tüm hakemler. Kabul öneren hakemlere küçük düzeltmede
// tekrar yük bindirilmez.
type PriorReview struct {
	ReviewerID     int64
	Recommendation string
}

func RevisionReviewers(decision string, prior []PriorReview) []int64 {
	var out []int64
	for _, p := range prior {
		if decision == "major_revision" || p.Recommendation != "accept" {
			out = append(out, p.ReviewerID)
		}
	}
	if len(out) == 0 { // herkes kabul önermişse en az bir hakem yine bakar
		for _, p := range prior {
			out = append(out, p.ReviewerID)
			break
		}
	}
	return out
}

// ---- süreler ---------------------------------------------------------------

// DueAt returns the response deadline for an assignment.
func DueAt(assigned time.Time) time.Time { return assigned.Add(ResponseDays * 24 * time.Hour) }

// DueReminders returns which reminder kinds are due (not yet expired).
func DueReminders(assigned, now time.Time) []string {
	var out []string
	for _, k := range []string{"day7", "day12"} {
		if now.Sub(assigned) >= time.Duration(ReminderDays[k])*24*time.Hour && now.Before(DueAt(assigned)) {
			out = append(out, k)
		}
	}
	return out
}

// Expired reports whether the assignment passed the 14-day window.
func Expired(assigned, now time.Time) bool { return !now.Before(DueAt(assigned)) }
