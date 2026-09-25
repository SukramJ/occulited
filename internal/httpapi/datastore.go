package httpapi

import (
	"net/http"

	"github.com/hobbyquaker/occulited/internal/store"
)

// DataStore is occulited's database file (openccu-lite task 214, internal/store): its storage
// mode and write interval, as occulited.json keeps them, and what the file is doing.
type DataStore interface {
	Status() store.Status
	// Set validates, applies at once and stores the setting; location is task 228's id + folder
	// ("" = the default on the userfs), never a share.
	Set(mode, interval, location string) error
}

// dataStoreBody is the PUT's body: the two settings, empty for the defaults.
type dataStoreBody struct {
	Mode         string `json:"mode"`
	SyncInterval string `json:"sync_interval"`
	Location     string `json:"location"`
}

func (a *SystemAPI) dataStoreGet(w http.ResponseWriter, _ *http.Request) {
	if a.DataStore == nil {
		writeJSON(w, http.StatusNotImplemented, apiError{Error: "unsupported", Message: "no database"})
		return
	}
	writeJSON(w, http.StatusOK, a.DataStore.Status())
}

// dataStorePut switches the mode and the interval at once: into ram the file is written a last
// time and closed, out of it the file is opened, merged with memory and written whole.
func (a *SystemAPI) dataStorePut(w http.ResponseWriter, r *http.Request) {
	if a.DataStore == nil {
		writeJSON(w, http.StatusNotImplemented, apiError{Error: "unsupported", Message: "no database"})
		return
	}
	var body dataStoreBody
	if err := readJSON(r, &body); err != nil {
		badBody(w, err)
		return
	}
	if err := a.DataStore.Set(body.Mode, body.SyncInterval, body.Location); err != nil {
		writeJSON(w, http.StatusUnprocessableEntity, apiError{Error: "invalid", Message: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, a.DataStore.Status())
}

// HistoryView is the datapoint history's list (openccu-lite task 195): what is recorded with its
// shape, the built-in defaults, the setting's additions and removals, and the figures.
type HistoryView struct {
	Datapoints    map[string]string `json:"datapoints"` // name -> sampled | event
	Defaults      []string          `json:"defaults"`
	Add           []string          `json:"add"`
	Remove        []string          `json:"remove"`
	Series        int               `json:"series"`
	Capped        uint64            `json:"capped"`
	RowsPerSeries int               `json:"rows_per_series"`
	MaxSeries     int               `json:"max_series"`
}

// HistorySetting is the list's setting: GET/PUT /datastore/history.
type HistorySetting interface {
	History() HistoryView
	SetHistory(add, remove []string) error
}

func (a *SystemAPI) historySetting() (HistorySetting, bool) {
	h, ok := a.DataStore.(HistorySetting)
	return h, ok && a.DataStore != nil
}

func (a *SystemAPI) dataStoreHistoryGet(w http.ResponseWriter, _ *http.Request) {
	h, ok := a.historySetting()
	if !ok {
		writeJSON(w, http.StatusNotImplemented, apiError{Error: "unsupported", Message: "no datapoint history"})
		return
	}
	writeJSON(w, http.StatusOK, h.History())
}

// dataStoreHistoryPut is PUT /datastore/history {add, remove}: datapoint names recorded in
// addition to the built-in list, and names of it that are not; applied at once, kept series
// stay readable.
func (a *SystemAPI) dataStoreHistoryPut(w http.ResponseWriter, r *http.Request) {
	h, ok := a.historySetting()
	if !ok {
		writeJSON(w, http.StatusNotImplemented, apiError{Error: "unsupported", Message: "no datapoint history"})
		return
	}
	var body struct {
		Add    []string `json:"add"`
		Remove []string `json:"remove"`
	}
	if err := readJSON(r, &body); err != nil {
		badBody(w, err)
		return
	}
	if err := h.SetHistory(body.Add, body.Remove); err != nil {
		writeJSON(w, http.StatusUnprocessableEntity, apiError{Error: "invalid", Message: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, h.History())
}
