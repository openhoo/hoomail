package httpserver

import (
	_ "embed"
	"net/http"
)

//go:generate go run openapi_generate.go

//go:embed openapi.json
var generatedOpenAPI []byte

func serveOpenAPI(response http.ResponseWriter, request *http.Request) {
	response.Header().Set("Cache-Control", "public, max-age=3600")
	response.Header().Set("Content-Type", "application/json")
	response.Header().Set("X-Content-Type-Options", "nosniff")
	response.WriteHeader(http.StatusOK)
	if request.Method != http.MethodHead {
		_, _ = response.Write(generatedOpenAPI)
	}
}
