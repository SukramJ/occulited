package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http/httptest"
	"testing"

	"github.com/hobbyquaker/occulited/internal/system"
)

// B-247: a staged update that would not fit is 422 no-space with the free and the required bytes
// (the Updates page words them); any other error is left to the route.
func TestNoUpdateSpace(t *testing.T) {
	rec := httptest.NewRecorder()
	if !noUpdateSpace(rec, fmt.Errorf("download: %w", &system.UpdateSpaceError{Free: 2298372096, Required: 2487167352})) {
		t.Fatal("not answered")
	}
	var out struct {
		Error  string           `json:"error"`
		Detail map[string]int64 `json:"detail"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if rec.Code != 422 || out.Error != "no-space" || out.Detail["free"] != 2298372096 || out.Detail["required"] != 2487167352 {
		t.Errorf("%d %s", rec.Code, rec.Body)
	}
	if noUpdateSpace(httptest.NewRecorder(), errors.New("zip: no *.img inside")) || noUpdateSpace(httptest.NewRecorder(), nil) {
		t.Error("another error answered as no-space")
	}
}
