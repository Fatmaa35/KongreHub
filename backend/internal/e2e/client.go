// Package e2e drives the running system end to end through its public API.
// `make verify` (cmd/verify) ve entegrasyon testleri aynı kontrolleri kullanır.
package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"strings"
	"sync/atomic"
	"time"
)

// Client is a tiny JSON HTTP client.
type Client struct {
	Base    string
	HTTP    *http.Client
	keySeq  atomic.Int64
	runID   string
	tokens  map[string]string
}

func NewClient(base string) *Client {
	return &Client{Base: strings.TrimRight(base, "/"), HTTP: &http.Client{Timeout: 90 * time.Second},
		runID: fmt.Sprintf("v%d", time.Now().UnixNano()), tokens: map[string]string{}}
}

// Resp is a decoded response.
type Resp struct {
	Status  int
	Body    []byte
	Header  http.Header
}

func (r *Resp) JSON(v any) error { return json.Unmarshal(r.Body, v) }

// M decodes into a generic map.
func (r *Resp) M() map[string]any {
	m := map[string]any{}
	_ = json.Unmarshal(r.Body, &m)
	return m
}

// ErrCode extracts error.code from the standard envelope.
func (r *Resp) ErrCode() string {
	var e struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	_ = json.Unmarshal(r.Body, &e)
	return e.Error.Code
}

// NewKey returns a unique Idempotency-Key.
func (c *Client) NewKey() string { return fmt.Sprintf("%s-%06d", c.runID, c.keySeq.Add(1)) }

// Do performs a request. body may be nil, []byte or any JSON-able value.
func (c *Client) Do(method, path, token string, body any, headers ...string) (*Resp, error) {
	var rd io.Reader
	ctype := "application/json"
	switch b := body.(type) {
	case nil:
	case []byte:
		rd = bytes.NewReader(b)
	case *multipartBody:
		rd = bytes.NewReader(b.buf.Bytes())
		ctype = b.ctype
	default:
		j, err := json.Marshal(b)
		if err != nil {
			return nil, err
		}
		rd = bytes.NewReader(j)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, method, c.Base+path, rd)
	if err != nil {
		return nil, err
	}
	if rd != nil {
		req.Header.Set("Content-Type", ctype)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	return &Resp{Status: resp.StatusCode, Body: b, Header: resp.Header}, nil
}

// Expect performs a request and fails unless the status matches.
func (c *Client) Expect(status int, method, path, token string, body any, headers ...string) (*Resp, error) {
	r, err := c.Do(method, path, token, body, headers...)
	if err != nil {
		return nil, fmt.Errorf("%s %s: %w", method, path, err)
	}
	if r.Status != status {
		return r, fmt.Errorf("%s %s: beklenen %d, gelen %d: %s", method, path, status, r.Status, trunc(r.Body))
	}
	return r, nil
}

func trunc(b []byte) string {
	if len(b) > 300 {
		return string(b[:300]) + "…"
	}
	return string(b)
}

// Login returns (and caches) an access token.
func (c *Client) Login(email, password string) (string, error) {
	if t, ok := c.tokens[email]; ok {
		return t, nil
	}
	r, err := c.Expect(200, "POST", "/api/auth/login", "", map[string]string{"email": email, "password": password})
	if err != nil {
		return "", err
	}
	var out struct {
		AccessToken string `json:"access_token"`
	}
	if err := r.JSON(&out); err != nil {
		return "", err
	}
	c.tokens[email] = out.AccessToken
	return out.AccessToken, nil
}

type multipartBody struct {
	buf   bytes.Buffer
	ctype string
}

// Multipart builds a multipart/form-data body with fields and one PDF file.
func Multipart(fields map[string]string, fileName string, file []byte) *multipartBody {
	mb := &multipartBody{}
	w := multipart.NewWriter(&mb.buf)
	for k, v := range fields {
		_ = w.WriteField(k, v)
	}
	if file != nil {
		h := make(textproto.MIMEHeader)
		h.Set("Content-Disposition", fmt.Sprintf(`form-data; name="file"; filename="%s"`, fileName))
		h.Set("Content-Type", "application/pdf")
		part, _ := w.CreatePart(h)
		_, _ = part.Write(file)
	}
	_ = w.Close()
	mb.ctype = w.FormDataContentType()
	return mb
}
