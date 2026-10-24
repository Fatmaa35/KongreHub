// Package pdfmeta removes identifying metadata from uploaded PDFs.
//
// Çift kör değerlendirmede yazar adı çoğu zaman PDF'in Info sözlüğünde
// (/Author, /Creator, /Title …) ve XMP akışında (dc:creator) saklanır.
// Dosya pdfcpu ile ayrıştırılır, Info sözlüğü ve katalog/sayfa düzeyindeki
// /Metadata ile /PieceInfo kaldırılır ve PDF baştan yazılır; yalnızca kökten
// erişilebilen nesneler yazıldığından eski Info nesnesi dosyada kalmaz.
package pdfmeta

import (
	"bytes"
	"errors"
	"fmt"

	"github.com/pdfcpu/pdfcpu/pkg/api"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"
)

var ErrNotPDF = errors.New("dosya geçerli bir PDF değil")

// Sanitize returns a metadata-free copy of the PDF.
func Sanitize(in []byte) ([]byte, error) {
	if !bytes.HasPrefix(bytes.TrimLeft(in, "\x00\t\r\n "), []byte("%PDF-")) {
		return nil, ErrNotPDF
	}
	conf := model.NewDefaultConfiguration()
	conf.ValidationMode = model.ValidationRelaxed
	ctx, err := api.ReadContext(bytes.NewReader(in), conf)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrNotPDF, err)
	}
	if err := api.ValidateContext(ctx); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrNotPDF, err)
	}
	if ctx.Encrypt != nil {
		return nil, errors.New("şifreli PDF kabul edilmiyor")
	}
	ctx.Info = nil
	ctx.Title, ctx.Author, ctx.Subject, ctx.Creator, ctx.Producer, ctx.Keywords = "", "", "", "", "", ""
	ctx.Properties = map[string]string{}
	if root, err := ctx.Catalog(); err == nil {
		root.Delete("Metadata")
		root.Delete("PieceInfo")
	}
	for i := 1; i <= ctx.PageCount; i++ {
		if d, _, _, err := ctx.PageDict(i, false); err == nil && d != nil {
			d.Delete("Metadata")
			d.Delete("PieceInfo")
		}
	}
	var out bytes.Buffer
	if err := api.WriteContext(ctx, &out); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// Author returns the /Author entry of a PDF ("" if none); used by tests and
// the leak checker to prove the sanitized file carries no identity.
func Author(in []byte) (string, error) {
	ctx, err := api.ReadContext(bytes.NewReader(in), model.NewDefaultConfiguration())
	if err != nil {
		return "", err
	}
	if ctx.Info == nil {
		return "", nil
	}
	d, err := ctx.DereferenceDict(*ctx.Info)
	if err != nil || d == nil {
		return "", err
	}
	if s := d.StringEntry("Author"); s != nil {
		return *s, nil
	}
	if o, ok := d.Find("Author"); ok && o != nil {
		return o.String(), nil
	}
	return "", nil
}
