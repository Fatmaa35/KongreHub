// Package pdffont returns fpdf documents with an embedded UTF-8 font
// (DejaVu Sans Condensed, serbest lisanslı) so Turkish characters render correctly.
package pdffont

import (
	_ "embed"

	"github.com/go-pdf/fpdf"
)

//go:embed DejaVuSansCondensed.ttf
var regular []byte

//go:embed DejaVuSansCondensed-Bold.ttf
var bold []byte

// Family is the font family name to use with SetFont.
const Family = "dejavu"

// New creates a document with the UTF-8 font registered (normal + B).
func New(orientation string) *fpdf.Fpdf {
	p := fpdf.New(orientation, "mm", "A4", "")
	p.AddUTF8FontFromBytes(Family, "", regular)
	p.AddUTF8FontFromBytes(Family, "B", bold)
	p.AddUTF8FontFromBytes(Family, "I", regular)
	return p
}
