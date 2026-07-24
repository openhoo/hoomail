package httpserver

import (
	_ "embed"
	"net/http"
)

//go:generate go run openapi_generate.go

//go:embed openapi.json
var generatedOpenAPI []byte

const swaggerUI = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>Hoomail API</title>
<link rel="stylesheet" href="https://unpkg.com/swagger-ui-dist@5.11.0/swagger-ui.css">
</head>
<body>
<div id="swagger-ui"></div>
<script src="https://unpkg.com/swagger-ui-dist@5.11.0/swagger-ui-bundle.js" crossorigin></script>
<script>
SwaggerUIBundle({url:"/openapi.json",dom_id:"#swagger-ui",deepLinking:true,validatorUrl:null});
</script>
</body>
</html>`

func serveOpenAPI(response http.ResponseWriter, request *http.Request) {
	response.Header().Set("Cache-Control", "public, max-age=3600")
	response.Header().Set("Content-Type", "application/json")
	response.Header().Set("X-Content-Type-Options", "nosniff")
	response.WriteHeader(http.StatusOK)
	if request.Method != http.MethodHead {
		_, _ = response.Write(generatedOpenAPI)
	}
}

func serveSwaggerUI(response http.ResponseWriter, request *http.Request) {
	response.Header().Set("Cache-Control", "public, max-age=3600")
	response.Header().Set("Content-Security-Policy", "default-src 'none'; connect-src 'self'; img-src data: https:; script-src 'unsafe-inline' https://unpkg.com; style-src 'unsafe-inline' https://unpkg.com")
	response.Header().Set("Content-Type", "text/html; charset=utf-8")
	response.Header().Set("Referrer-Policy", "no-referrer")
	response.Header().Set("X-Content-Type-Options", "nosniff")
	response.WriteHeader(http.StatusOK)
	if request.Method != http.MethodHead {
		_, _ = response.Write([]byte(swaggerUI))
	}
}
