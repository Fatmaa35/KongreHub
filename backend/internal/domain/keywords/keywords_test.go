package keywords

import (
	"reflect"
	"testing"
)

func TestNormalize(t *testing.T) {
	got := Normalize([]string{"  Machine   Learning ", "machine_learning", "NLP", "", "nlp"})
	want := []string{"machine learning", "nlp"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestCoverageAndJaccard(t *testing.T) {
	p := NewSet([]string{"a", "b", "c", "d"})
	e := NewSet([]string{"a", "b", "x"})
	if c := Coverage(p, e); c != 0.5 {
		t.Fatalf("coverage %v", c)
	}
	if c := Coverage(NewSet(nil), e); c != 0 {
		t.Fatalf("empty coverage %v", c)
	}
	if j := Jaccard(p, e); j != 2.0/5.0 {
		t.Fatalf("jaccard %v", j)
	}
	if j := Jaccard(NewSet(nil), NewSet(nil)); j != 0 {
		t.Fatalf("empty jaccard %v", j)
	}
}
