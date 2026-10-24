package app

import (
	"net/http"
	"strconv"

	"github.com/fatmakaplan/kongrehub/api"
)

func serveOpenAPI(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/yaml; charset=utf-8")
	_, _ = w.Write(api.OpenAPI)
}

// serveDocs renders Swagger UI (CDN) for the embedded OpenAPI document.
func serveDocs(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(`<!doctype html><html><head><meta charset="utf-8"><title>KongreHub API</title>
<link rel="stylesheet" href="https://cdn.jsdelivr.net/npm/swagger-ui-dist@5/swagger-ui.css"></head>
<body><div id="ui"></div><script src="https://cdn.jsdelivr.net/npm/swagger-ui-dist@5/swagger-ui-bundle.js"></script>
<script>SwaggerUIBundle({url:'/openapi.yaml',dom_id:'#ui'})</script></body></html>`))
}

func strconvF(f float64) string        { return strconv.FormatFloat(f, 'f', -1, 64) }
func parseF(s string) (float64, error) { return strconv.ParseFloat(s, 64) }
