package httpapi

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io/fs"
	"net/http"
	"net/url"
	"strconv"

	"github.com/hobbyquaker/occulited/internal/addonimage"
	"github.com/hobbyquaker/occulited/internal/auth"
	"github.com/hobbyquaker/occulited/internal/system"
)

// The addon icons and logos (openccu-lite task 100). An addon declares them in its manifest
// (ui.icon, ui.icon_dark, ui.logo, ui.logo_dark); the shell shows them from occulited's own origin
// and through <img> alone, never from the addon's own pages and never inlined:
//
//	GET /addons/{id}/images/{kind}   an installed addon's image, out of its tree (system.Root.AddonImage)
//	GET /catalog/{id}/images/{kind}  a catalogue entry's image, the copy the check fetched with the manifest
//
// {kind} is icon, icon-dark, logo or logo-dark. Every answer carries the type the content says,
// nosniff, a Content-Security-Policy that lets nothing in an SVG run or load, and an ETag; an
// addon that declares no such image, or whose file is not there or is no image, is a 404. GET
// /addons, GET /services and GET /catalog name each addon's images as `images`, kind → URL, so
// the shell knows which to ask for and asks for nothing else.

func (a *SystemAPI) registerAddonImages(mux *http.ServeMux, p string) {
	route(mux, auth.ScopeSystemRead, "GET "+p+"/addons/{id}/images/{kind}", a.addonImage)
	route(mux, auth.ScopeSystemRead, "GET "+p+"/catalog/{id}/images/{kind}", a.catalogImage)
}

// addonImageURLs is the `images` field of an installed addon: kind → URL, for the kinds its stored
// manifest declares; nil for none. The version is in the URL so that an update is a new URL (the
// answer is cached for an hour).
func addonImageURLs(root system.Root, id, version string) map[string]string {
	kinds := root.AddonImageKinds(id)
	if len(kinds) == 0 {
		return nil
	}
	out := make(map[string]string, len(kinds))
	for _, k := range kinds {
		out[k] = "/api/system/v1/addons/" + url.PathEscape(id) + "/images/" + k + "?v=" + url.QueryEscape(version)
	}
	return out
}

// catalogImageURLs is the `images` field of a catalogue item: kind → URL, with the image's hash
// as the version.
func catalogImageURLs(id string, hashes map[string]string) map[string]string {
	if len(hashes) == 0 {
		return nil
	}
	out := make(map[string]string, len(hashes))
	for k, h := range hashes {
		if len(h) > 12 {
			h = h[:12]
		}
		out[k] = "/api/system/v1/catalog/" + url.PathEscape(id) + "/images/" + k + "?v=" + url.QueryEscape(h)
	}
	return out
}

func (a *SystemAPI) addonImage(w http.ResponseWriter, r *http.Request) {
	b, _, err := a.Root.AddonImage(r.PathValue("id"), r.PathValue("kind"))
	writeImage(w, r, b, err)
}

func (a *SystemAPI) catalogImage(w http.ResponseWriter, r *http.Request) {
	if a.Catalog == nil {
		writeJSON(w, http.StatusNotImplemented, apiError{Error: "unsupported", Message: "the catalogue is switched off"})
		return
	}
	b, err := a.Catalog.Image(r.PathValue("id"), r.PathValue("kind"))
	writeImage(w, r, b, err)
}

// writeImage answers an image with addonimage's headers and an ETag, 304 when the browser has it, 404
// for one that is not there or cannot be served (the reason is logged by the reader, not told: the
// shell falls back to the next image either way).
func writeImage(w http.ResponseWriter, r *http.Request, b []byte, err error) {
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			writeJSON(w, http.StatusNotFound, apiError{Error: "not-found", Message: "the addon declares no such image"})
			return
		}
		writeJSON(w, http.StatusNotFound, apiError{Error: "not-found", Message: err.Error()})
		return
	}
	ct, ok := addonimage.Sniff(b)
	if !ok {
		writeJSON(w, http.StatusNotFound, apiError{Error: "not-found", Message: "the file is not an image"})
		return
	}
	sum := sha256.Sum256(b)
	etag := `"` + hex.EncodeToString(sum[:8]) + `"`
	addonimage.SetHeaders(w.Header(), ct)
	w.Header().Set("ETag", etag)
	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Header().Set("Content-Length", strconv.Itoa(len(b)))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(b)
}
