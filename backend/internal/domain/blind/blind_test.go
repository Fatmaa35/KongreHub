package blind

import "testing"

func TestVisibilityMatrix(t *testing.T) {
	cases := []struct {
		role              string
		authors, reviewer bool
	}{
		{Author, true, false}, {Reviewer, false, false}, {Editor, true, true},
		{Admin, true, true}, {Registrar, false, false}, {Attendee, false, false},
	}
	for _, c := range cases {
		if CanSeeAuthors(c.role) != c.authors || CanSeeReviewers(c.role) != c.reviewer {
			t.Errorf("%s: authors=%v reviewers=%v", c.role, CanSeeAuthors(c.role), CanSeeReviewers(c.role))
		}
	}
}

func TestReviewerLabelsStable(t *testing.T) {
	l := ReviewerLabels([]int64{42, 7, 19})
	if l[7] != "Hakem A" || l[19] != "Hakem B" || l[42] != "Hakem C" {
		t.Fatalf("%v", l)
	}
	ids := make([]int64, 28)
	for i := range ids {
		ids[i] = int64(i + 1)
	}
	m := ReviewerLabels(ids)
	if m[26] != "Hakem Z" || m[27] != "Hakem AA" || m[28] != "Hakem AB" {
		t.Fatalf("%s %s %s", m[26], m[27], m[28])
	}
}

func TestScan(t *testing.T) {
	body := []byte(`{"title":"x","comments":"see work by AYSE yilmaz","email":"a@b.c"}`)
	f := Scan(body, map[string]string{"Ayse Yilmaz": "author_name", "ab": "short", "kemal@uni.edu": "email"})
	if len(f) != 1 || f[0].Kind != "author_name" {
		t.Fatalf("%v", f)
	}
}
