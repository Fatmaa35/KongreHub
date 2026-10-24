package pdfmeta

import (
	"bytes"
	"testing"

	"github.com/go-pdf/fpdf"
)

func samplePDF(t *testing.T, author string) []byte {
	t.Helper()
	p := fpdf.New("P", "mm", "A4", "")
	p.SetAuthor(author, false)
	p.SetCreator(author+" Lab", false)
	p.SetTitle("Paper by "+author, false)
	p.AddPage()
	p.SetFont("Helvetica", "", 12)
	p.Cell(40, 10, "Anonymous submission body")
	var b bytes.Buffer
	if err := p.Output(&b); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func TestSanitizeRemovesAuthor(t *testing.T) {
	in := samplePDF(t, "Ayse Yilmaz")
	if a, err := Author(in); err != nil || a == "" {
		t.Fatalf("fixture should carry author, got %q %v", a, err)
	}
	if !bytes.Contains(in, []byte("Yilmaz")) {
		t.Fatal("fixture should contain the name in raw bytes")
	}
	out, err := Sanitize(in)
	if err != nil {
		t.Fatal(err)
	}
	if a, _ := Author(out); a != "" {
		t.Fatalf("author still present: %q", a)
	}
	if bytes.Contains(out, []byte("Yilmaz")) || bytes.Contains(out, []byte("Ayse")) {
		t.Fatal("name still present somewhere in the bytes")
	}
}

func TestSanitizeRejectsNonPDF(t *testing.T) {
	if _, err := Sanitize([]byte("hello world")); err == nil {
		t.Fatal("expected error")
	}
	if _, err := Sanitize([]byte("%PDF-1.4 garbage")); err == nil {
		t.Fatal("expected error on corrupt pdf")
	}
}
