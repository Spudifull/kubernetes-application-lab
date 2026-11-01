package app

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Spudifull/kubernetes-application-lab/internal/database"
)

func TestDatabaseUnavailableDoesNotKillProcess(t *testing.T) {
	db, err := database.Open(database.Config{Host: "127.0.0.1", Port: "1", Name: "lab", User: "lab", Password: "not-logged"})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	a := NewWithOptions(BuildInfo{}, Options{Database: db})
	for _, tc := range []struct {
		path   string
		status int
	}{{"/livez", 200}, {"/readyz", 503}, {"/records/key", 503}} {
		r := httptest.NewRecorder()
		a.Handler().ServeHTTP(r, httptest.NewRequest(http.MethodGet, tc.path, nil))
		if r.Code != tc.status {
			t.Errorf("%s: got %d, want %d", tc.path, r.Code, tc.status)
		}
	}
}

func TestDatabaseRecordInputBounds(t *testing.T) {
	db, err := database.Open(database.Config{Host: "127.0.0.1", Port: "1", Name: "lab", User: "lab", Password: "not-logged"})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	a := NewWithOptions(BuildInfo{}, Options{Database: db})
	for _, tc := range []struct{ key, body string }{{"invalid!", `{"value":"x"}`}, {"valid", `{"value":"` + strings.Repeat("x", 513) + `"}`}, {"valid", `{"value":"x","unexpected":1}`}} {
		r := httptest.NewRecorder()
		a.Handler().ServeHTTP(r, httptest.NewRequest(http.MethodPost, "/records/"+tc.key, strings.NewReader(tc.body)))
		if r.Code != http.StatusBadRequest {
			t.Errorf("key=%q: got %d", tc.key, r.Code)
		}
	}
}
