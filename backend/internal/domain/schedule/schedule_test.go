package schedule

import (
	"fmt"
	"math/rand/v2"
	"testing"
	"time"

	"github.com/fatmakaplan/kongrehub/internal/domain/keywords"
)

var ist, _ = time.LoadLocation("Europe/Istanbul")
var tokyo, _ = time.LoadLocation("Asia/Tokyo")
var ny, _ = time.LoadLocation("America/New_York")
var la, _ = time.LoadLocation("America/Los_Angeles")

func slotsFor(days int) []Slot {
	var out []Slot
	id := int64(1)
	starts := [][2]int{{9, 0}, {11, 0}, {14, 0}, {16, 0}, {18, 0}}
	for d := 0; d < days; d++ {
		for _, st := range starts {
			s := time.Date(2027, 5, 10+d, st[0], st[1], 0, 0, ist)
			out = append(out, Slot{ID: id, Start: s, End: s.Add(90 * time.Minute)})
			id++
		}
	}
	return out
}

func rooms() []Room {
	return []Room{{1, "A", 40}, {2, "B", 60}, {3, "C", 80}, {4, "D", 120}, {5, "E", 200}, {6, "F", 300}}
}

// instance: n kabul edilmiş bildiri; %10 ödenmemiş; konuşmacıların bir kısmı uzaktan.
func instance(seed uint64, n int) Input {
	rng := rand.New(rand.NewPCG(seed, 99))
	in := Input{Rooms: rooms(), Slots: slotsFor(3), People: map[int64]Person{}}
	locs := []*time.Location{ist, ist, ist, ist, ist, ist, tokyo, ny, la}
	for i := 0; i < n; i++ {
		t := rng.IntN(8)
		kws := []string{fmt.Sprintf("topic%d", t), fmt.Sprintf("topic%d-sub%d", t, rng.IntN(3)), fmt.Sprintf("m%d", rng.IntN(20))}
		presenter := int64(10000 + i)
		if i%17 == 0 && i > 0 { // aynı konuşmacının ikinci bildirisi
			presenter = int64(10000 + i - 1)
		}
		loc := locs[rng.IntN(len(locs))]
		in.People[presenter] = Person{ID: presenter, Location: loc, StartHour: 8, EndHour: 22}
		in.Papers = append(in.Papers, Paper{
			ID: int64(i + 1), Keywords: keywords.NewSet(kws), PresenterID: presenter,
			AuthorIDs: []int64{presenter, int64(20000 + i)}, Minutes: []int{15, 20, 20, 30}[rng.IntN(4)],
			Audience: 20 + rng.IntN(200), Paid: rng.IntN(10) != 0,
		})
	}
	for c := 0; c < 30; c++ {
		in.Chairs = append(in.Chairs, Chair{ID: int64(30000 + c), Keywords: keywords.NewSet([]string{fmt.Sprintf("topic%d", c%8)})})
	}
	// bir başkan aynı zamanda bir bildirinin yazarı (çatışma kontrolü için)
	in.Chairs = append(in.Chairs, Chair{ID: 20003, Keywords: keywords.NewSet([]string{"topic1"})})
	return in
}

func TestAvailabilityAcrossTimezones(t *testing.T) {
	morning := time.Date(2027, 5, 10, 9, 0, 0, 0, ist)  // 15:00 Tokyo, 02:00 NY, 23:00 LA
	evening := time.Date(2027, 5, 10, 18, 0, 0, 0, ist) // 00:00 Tokyo, 11:00 NY, 08:00 LA
	p := func(l *time.Location) Person { return Person{Location: l, StartHour: 8, EndHour: 22} }
	cases := []struct {
		loc  *time.Location
		s    time.Time
		want bool
	}{
		{ist, morning, true}, {tokyo, morning, true}, {ny, morning, false}, {la, morning, false},
		{ist, evening, true}, {tokyo, evening, false}, {ny, evening, true}, {la, evening, true},
	}
	for _, c := range cases {
		if got := Available(p(c.loc), c.s, c.s.Add(20*time.Minute)); got != c.want {
			t.Errorf("%s at %s: got %v want %v", c.loc, c.s.In(c.loc).Format("15:04"), got, c.want)
		}
	}
	// gece yarısında biten konuşma, pencere 24'e kadar ise geçerli
	mid := Person{Location: ist, StartHour: 20, EndHour: 24}
	if !Available(mid, time.Date(2027, 5, 10, 23, 40, 0, 0, ist), time.Date(2027, 5, 11, 0, 0, 0, 0, ist)) {
		t.Error("talk ending at midnight")
	}
	if Available(mid, time.Date(2027, 5, 10, 23, 50, 0, 0, ist), time.Date(2027, 5, 11, 0, 10, 0, 0, ist)) {
		t.Error("talk crossing midnight")
	}
}

func TestBuildProducesConflictFreeProgram(t *testing.T) {
	for _, seed := range []uint64{1, 2, 3, 42, 2027} {
		in := instance(seed, 80)
		prog := Build(in)
		if v := Validate(in, prog); len(v) > 0 {
			t.Fatalf("seed %d: %d violations, e.g. %s", seed, len(v), v[0])
		}
		paid := 0
		for _, p := range in.Papers {
			if p.Paid {
				paid++
			}
		}
		t.Logf("seed %d: %d/%d paid papers scheduled in %d sessions, sim=%.3f, nodes=%d, %dms, unscheduled=%d",
			seed, prog.Stats.Scheduled, paid, prog.Stats.Sessions, prog.Stats.AvgSimilarity, prog.Stats.SearchNodes, prog.Stats.DurationMS, len(prog.Unscheduled))
		if prog.Stats.Scheduled < paid*9/10 {
			t.Fatalf("seed %d: too few scheduled (%d of %d)", seed, prog.Stats.Scheduled, paid)
		}
	}
}

func TestUnpaidPapersExcluded(t *testing.T) {
	in := instance(5, 30)
	prog := Build(in)
	unpaid := map[int64]bool{}
	for _, p := range in.Papers {
		if !p.Paid {
			unpaid[p.ID] = true
		}
	}
	for _, s := range prog.Sessions {
		for _, tk := range s.Talks {
			if unpaid[tk.PaperID] {
				t.Fatalf("unpaid paper %d scheduled", tk.PaperID)
			}
		}
	}
	got := 0
	for _, u := range prog.Unscheduled {
		if u.Reason == ReasonUnpaid {
			got++
		}
	}
	if got != len(unpaid) {
		t.Fatalf("unpaid listed %d want %d", got, len(unpaid))
	}
}

func TestRemoteSpeakerPlacedInFeasibleSlot(t *testing.T) {
	in := Input{Rooms: rooms(), Slots: slotsFor(1), People: map[int64]Person{
		1: {1, la, 8, 22}, 2: {2, tokyo, 8, 22}, 3: {3, ist, 8, 22},
	}, Chairs: []Chair{{100, keywords.NewSet([]string{"x"})}, {101, keywords.NewSet([]string{"y"})}, {102, nil}}}
	in.Papers = []Paper{
		{ID: 1, Keywords: keywords.NewSet([]string{"x"}), PresenterID: 1, AuthorIDs: []int64{1}, Minutes: 20, Audience: 30, Paid: true},
		{ID: 2, Keywords: keywords.NewSet([]string{"x"}), PresenterID: 2, AuthorIDs: []int64{2}, Minutes: 20, Audience: 30, Paid: true},
		{ID: 3, Keywords: keywords.NewSet([]string{"y"}), PresenterID: 3, AuthorIDs: []int64{3}, Minutes: 20, Audience: 30, Paid: true},
	}
	prog := Build(in)
	if v := Validate(in, prog); len(v) > 0 {
		t.Fatalf("%v", v)
	}
	if prog.Stats.Scheduled != 3 {
		t.Fatalf("all 3 should be scheduled: %+v", prog)
	}
	// LA ve Tokyo konuşmacıları ortak pencereye sahip değil → aynı oturumda olamazlar
	for _, s := range prog.Sessions {
		ids := map[int64]bool{}
		for _, tk := range s.Talks {
			ids[tk.PaperID] = true
			if tk.PaperID == 1 && tk.Start.In(ist).Hour() < 18 {
				t.Fatalf("LA speaker placed at %s Istanbul", tk.Start.In(ist).Format("15:04"))
			}
		}
		if ids[1] && ids[2] {
			t.Fatal("LA and Tokyo speakers cannot share a slot")
		}
	}
}

func TestImpossibleSpeakerReported(t *testing.T) {
	hawaii, _ := time.LoadLocation("Pacific/Honolulu") // İstanbul 09:00–19:30 = Honolulu 20:00–06:30
	in := Input{Rooms: rooms(), Slots: slotsFor(1)[:4], People: map[int64]Person{1: {1, hawaii, 8, 18}},
		Chairs: []Chair{{100, nil}}}
	in.Papers = []Paper{{ID: 1, Keywords: keywords.NewSet([]string{"x"}), PresenterID: 1, Minutes: 20, Audience: 10, Paid: true}}
	prog := Build(in)
	if len(prog.Unscheduled) != 1 || prog.Unscheduled[0].Reason != ReasonAvailability {
		t.Fatalf("%+v", prog.Unscheduled)
	}
	if v := Validate(in, prog); len(v) > 0 {
		t.Fatalf("%v", v)
	}
}

func TestCapacityRespectedAndReported(t *testing.T) {
	in := Input{Rooms: []Room{{1, "Küçük", 50}}, Slots: slotsFor(1)[:1], People: map[int64]Person{}, Chairs: []Chair{{100, nil}}}
	in.Papers = []Paper{{ID: 1, Keywords: keywords.NewSet([]string{"x"}), PresenterID: 1, Minutes: 20, Audience: 400, Paid: true}}
	prog := Build(in)
	if len(prog.Sessions) != 0 || len(prog.Unscheduled) != 1 || prog.Unscheduled[0].Reason != ReasonNoRoom {
		t.Fatalf("%+v", prog)
	}
}

func TestSimilarPapersGroupedTogether(t *testing.T) {
	in := Input{Rooms: rooms(), Slots: slotsFor(1), People: map[int64]Person{}, Chairs: []Chair{{100, nil}, {101, nil}}}
	for i := 1; i <= 8; i++ {
		topic := "graphs"
		if i > 4 {
			topic = "proteins"
		}
		in.Papers = append(in.Papers, Paper{ID: int64(i), Keywords: keywords.NewSet([]string{topic, topic + "-x"}), PresenterID: int64(i), Minutes: 20, Audience: 30, Paid: true})
	}
	prog := Build(in)
	if len(prog.Sessions) != 2 || prog.Stats.AvgSimilarity != 1 {
		t.Fatalf("expected 2 pure sessions, got %d sim %.2f", len(prog.Sessions), prog.Stats.AvgSimilarity)
	}
}

func TestValidatorDetectsEveryViolation(t *testing.T) {
	in := instance(1, 20)
	for i := range in.Papers {
		in.Papers[i].Paid = true
	}
	s := in.Slots[0]
	p1, p2, p3 := in.Papers[0], in.Papers[1], in.Papers[2]
	big := Paper{ID: 999, PresenterID: p1.PresenterID, Minutes: 20, Audience: 1000, Paid: false}
	in.Papers = append(in.Papers, big)
	in.People[p1.PresenterID] = Person{p1.PresenterID, ist, 8, 22}
	in.People[p3.PresenterID] = Person{p3.PresenterID, la, 8, 22}
	tk := func(p Paper, off int) Talk {
		st := s.Start.Add(time.Duration(off) * time.Minute)
		return Talk{p.ID, st, st.Add(time.Duration(p.Minutes) * time.Minute)}
	}
	prog := Program{Sessions: []Session{
		{RoomID: 1, SlotID: s.ID, ChairID: p2.AuthorIDs[1], Talks: []Talk{tk(p1, 0), tk(p2, 5)}}, // çakışan konuşmalar + başkan çatışması
		{RoomID: 1, SlotID: s.ID, ChairID: 30001, Talks: []Talk{tk(big, 0), tk(p1, 30)}},         // salon çift, kişi çift, kapasite, ödenmemiş, mükerrer
		{RoomID: 2, SlotID: s.ID, Talks: []Talk{tk(p3, 0)}},                                      // başkansız + LA uygunluğu
		{RoomID: 3, SlotID: s.ID, ChairID: 30002, Talks: []Talk{{p2.ID, s.Start.Add(80 * time.Minute), s.Start.Add(100 * time.Minute)}}},
	}}
	v := Validate(in, prog)
	codes := map[string]bool{}
	for _, x := range v {
		codes[x.Code] = true
	}
	for _, want := range []string{"talk_overlap", "chair_not_candidate", "room_double_booked", "person_double_booked", "capacity", "unpaid", "duplicate_paper", "no_chair", "availability", "outside_slot", "missing_paper"} {
		if !codes[want] {
			t.Errorf("validator missed %s; got %v", want, v)
		}
	}
	// başkan çatışması (yazar başkan) ayrıca
	in.Chairs = append(in.Chairs, Chair{ID: p2.AuthorIDs[1]})
	v = Validate(in, prog)
	found := false
	for _, x := range v {
		if x.Code == "chair_conflict" {
			found = true
		}
	}
	if !found {
		t.Error("chair_conflict not detected")
	}
}
