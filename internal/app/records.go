package app

import (
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"regexp"
	"unicode/utf8"

	"github.com/Spudifull/kubernetes-application-lab/internal/database"
)

var recordKey = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,64}$`)

func (a *App) handleRecordPut(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("key")
	if !recordKey.MatchString(key) {
		http.Error(w, "invalid record key", http.StatusBadRequest)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1024)
	defer r.Body.Close()
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	var body struct {
		Value string `json:"value"`
	}
	if err := decoder.Decode(&body); err != nil || !utf8.ValidString(body.Value) || utf8.RuneCountInString(body.Value) > 512 {
		http.Error(w, "invalid record body", http.StatusBadRequest)
		return
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		http.Error(w, "invalid record body", http.StatusBadRequest)
		return
	}
	if err := database.Put(r.Context(), a.database, key, body.Value); err != nil {
		a.logger.Warn("database_write_failed", "pod", a.podName)
		http.Error(w, "database unavailable", http.StatusServiceUnavailable)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *App) handleRecordGet(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("key")
	if !recordKey.MatchString(key) {
		http.Error(w, "invalid record key", http.StatusBadRequest)
		return
	}
	value, err := database.Get(r.Context(), a.database, key)
	if errors.Is(err, sql.ErrNoRows) {
		http.Error(w, "record not found", http.StatusNotFound)
		return
	}
	if err != nil {
		a.logger.Warn("database_read_failed", "pod", a.podName)
		http.Error(w, "database unavailable", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(struct {
		Key   string `json:"key"`
		Value string `json:"value"`
	}{key, value})
}
