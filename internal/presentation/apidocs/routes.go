// Package apidocs serves the OpenAPI document and a Swagger UI page for it.
package apidocs

import (
	"crypto/sha256"
	"encoding/base64"
	"net/http"

	"keyloop-coding-challenge/api"
)

// The Swagger UI assets come from the jsDelivr CDN, pinned to one version and checked
// with Subresource Integrity, so a changed file is refused by the browser instead of
// running on the API's origin (where the unauthenticated command endpoints live).
const (
	swaggerUIBase      = "https://cdn.jsdelivr.net/npm/swagger-ui-dist@5.17.14/"
	swaggerUICSSSRI    = "sha384-wxLW6kwyHktdDGr6Pv1zgm/VGJh99lfUbzSn6HNHBENZlCN7W602k9VkGdxuFvPn"
	swaggerUIBundleSRI = "sha384-wmyclcVGX/WhUkdkATwhaK1X1JtiNrr2EoYJ+diV3vj4v6OC5yCeSu+yW13SYJep"
	initScript         = `window.ui = SwaggerUIBundle({url: "/openapi.yaml", dom_id: "#swagger-ui"});`
)

// The page is static: it embeds no request data.
var (
	swaggerPage = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<title>Inventory API</title>
<link rel="stylesheet" href="` + swaggerUIBase + `swagger-ui.css" integrity="` + swaggerUICSSSRI + `" crossorigin="anonymous">
</head>
<body>
<div id="swagger-ui"></div>
<script src="` + swaggerUIBase + `swagger-ui-bundle.js" integrity="` + swaggerUIBundleSRI + `" crossorigin="anonymous"></script>
<script>` + initScript + `</script>
</body>
</html>
`
	// The only inline script allowed is initScript (by hash). Swagger UI sets inline
	// styles and data: images; it fetches only /openapi.yaml.
	contentSecurityPolicy = "default-src 'none'; " +
		"script-src https://cdn.jsdelivr.net 'sha256-" + scriptHash(initScript) + "'; " +
		"style-src https://cdn.jsdelivr.net 'unsafe-inline'; " +
		"img-src 'self' data:; " +
		"connect-src 'self'; " +
		"base-uri 'none'; form-action 'none'; frame-ancestors 'none'"
)

// Register adds GET /openapi.yaml and GET /docs.
func Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /openapi.yaml", serveSpec)
	mux.HandleFunc("GET /docs", serveSwaggerUI)
}

func serveSpec(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/yaml")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = w.Write(api.OpenAPI)
}

func serveSwaggerUI(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", contentSecurityPolicy)
	_, _ = w.Write([]byte(swaggerPage))
}

func scriptHash(script string) string {
	sum := sha256.Sum256([]byte(script))
	return base64.StdEncoding.EncodeToString(sum[:])
}
