package pdfmeta

import (
	"os"
	"testing"
)

// GEN_SAMPLE=1 ile scripts/sample.pdf (yazar metadata'sı içeren örnek) üretilir.
func TestGenerateSamplePDF(t *testing.T) {
	if os.Getenv("GEN_SAMPLE") == "" {
		t.Skip("GEN_SAMPLE ayarlı değil")
	}
	if err := os.WriteFile("../../../scripts/sample.pdf", samplePDF(t, "Ayse Yilmaz"), 0o644); err != nil {
		t.Fatal(err)
	}
}
