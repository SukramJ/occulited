package httpapi

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"strings"
)

// GET /sbom (task 179): the image's SBOM for the Licences page, without a session (nothing in it
// is secret, and the page is reachable from the login screen). The file is served as it lies on
// disk, gzip, to every client that accepts gzip - which every browser does; one that does not
// gets it unpacked. The ETag is the file's hash, so the page loads it once per image.
// ?download=1 makes it an attachment.
func (a *SystemAPI) sbom(w http.ResponseWriter, r *http.Request) {
	data, err := a.Root.ReadSBOM()
	if errors.Is(err, fs.ErrNotExist) {
		writeJSON(w, http.StatusNotFound, apiError{Error: "not-found", Message: "this image carries no SBOM"})
		return
	}
	if err != nil {
		writeErr(w, err)
		return
	}
	sum := sha256.Sum256(data)
	etag := `"` + hex.EncodeToString(sum[:8]) + `"`
	h := w.Header()
	h.Set("ETag", etag)
	h.Set("Cache-Control", "no-cache")
	h.Set("Vary", "Accept-Encoding")
	h.Set("Content-Type", "application/vnd.cyclonedx+json")
	if r.URL.Query().Get("download") == "1" {
		h.Set("Content-Disposition", `attachment; filename="openccu-lite.cdx.json"`)
	}
	if match := r.Header.Get("If-None-Match"); match != "" && strings.Contains(match, etag) {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	if acceptsGzip(r) {
		h.Set("Content-Encoding", "gzip")
		_, _ = w.Write(data)
		return
	}
	zr, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		writeErr(w, err)
		return
	}
	defer zr.Close()
	_, _ = io.Copy(w, zr)
}

func acceptsGzip(r *http.Request) bool {
	for _, part := range strings.Split(r.Header.Get("Accept-Encoding"), ",") {
		name, q, _ := strings.Cut(strings.TrimSpace(part), ";")
		if strings.EqualFold(strings.TrimSpace(name), "gzip") && strings.ReplaceAll(strings.TrimSpace(q), " ", "") != "q=0" {
			return true
		}
	}
	return false
}
