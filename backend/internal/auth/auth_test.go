package auth

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestPasswordHashing(t *testing.T) {
	h, err := HashPassword("Demo123!")
	if err != nil || !strings.HasPrefix(h, "$argon2id$v=19$m=19456,t=2,p=1$") {
		t.Fatalf("%s %v", h, err)
	}
	if ok, _ := VerifyPassword("Demo123!", h); !ok {
		t.Fatal("correct password rejected")
	}
	if ok, _ := VerifyPassword("demo123!", h); ok {
		t.Fatal("wrong password accepted")
	}
	h2, _ := HashPassword("Demo123!")
	if h == h2 {
		t.Fatal("salt must differ")
	}
	if _, err := VerifyPassword("x", "$bcrypt$..."); err == nil {
		t.Fatal("bad hash format")
	}
}

func TestTokens(t *testing.T) {
	tk := NewTokens(strings.Repeat("s", 32), time.Minute)
	s, _, err := tk.Access(Principal{UserID: 5, Role: "editor", Name: "E"})
	if err != nil {
		t.Fatal(err)
	}
	p, err := tk.Parse(s)
	if err != nil || p.UserID != 5 || p.Role != "editor" {
		t.Fatalf("%+v %v", p, err)
	}
	other := NewTokens(strings.Repeat("x", 32), time.Minute)
	if _, err := other.Parse(s); err == nil {
		t.Fatal("foreign secret accepted")
	}
	tk.now = func() time.Time { return time.Now().Add(2 * time.Minute) }
	if _, err := tk.Parse(s); err == nil {
		t.Fatal("expired token accepted")
	}
	r1, h1, _ := NewRefreshToken()
	if string(HashRefresh(r1)) != string(h1) {
		t.Fatal("refresh hash")
	}
}

func TestRequireMiddleware(t *testing.T) {
	tk := NewTokens(strings.Repeat("s", 32), time.Minute)
	ok := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) })
	h := tk.Middleware(Require("editor")(ok))
	call := func(auth string) int {
		r := httptest.NewRequest("GET", "/", nil)
		if auth != "" {
			r.Header.Set("Authorization", auth)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w.Code
	}
	ed, _, _ := tk.Access(Principal{UserID: 1, Role: "editor"})
	au, _, _ := tk.Access(Principal{UserID: 2, Role: "author"})
	if c := call(""); c != 401 {
		t.Errorf("anon %d", c)
	}
	if c := call("Bearer junk"); c != 401 {
		t.Errorf("junk %d", c)
	}
	if c := call("Basic abc"); c != 401 {
		t.Errorf("basic %d", c)
	}
	if c := call("Bearer " + au); c != 403 {
		t.Errorf("author %d", c)
	}
	if c := call("Bearer " + ed); c != 204 {
		t.Errorf("editor %d", c)
	}
}
