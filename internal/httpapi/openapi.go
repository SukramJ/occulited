package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"
	"sync"

	"github.com/hobbyquaker/occulited/docs"
)

// GET /api/openapi.json (openccu-lite task 298): the OpenAPI document of this API, as committed in
// docs/openapi.json and published with each release, with the running binary's version. Behind
// system:read: routes and scopes are public in the repository, the exact version of a system is
// not something an unauthenticated caller needs. No viewer is served (the maintainer's decision).

var (
	openAPIOnce sync.Once
	openAPIBody []byte
	openAPIErr  error
)

// openAPIDocument is the committed document with info.version set to this build's, compacted.
func openAPIDocument() ([]byte, error) {
	openAPIOnce.Do(func() {
		var doc map[string]any
		if openAPIErr = json.Unmarshal(docs.OpenAPI, &doc); openAPIErr != nil {
			return
		}
		if info, ok := doc["info"].(map[string]any); ok {
			info["version"] = strings.TrimPrefix(Implementation, "occulited ")
		}
		openAPIBody, openAPIErr = json.Marshal(doc)
	})
	return openAPIBody, openAPIErr
}

func (a *SystemAPI) openAPI(w http.ResponseWriter, _ *http.Request) {
	b, err := openAPIDocument()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, apiError{Error: "internal", Message: err.Error()})
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(append(b, '\n'))
}
