package api

import (
	"bytes"
	"encoding/json"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"

	"github.com/wyw14/solo-035/internal/service"
)

func captionHandler(t *testing.T) (http.Handler, string) {
	t.Helper()
	runtime, err := service.NewRuntime(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Bootstrap(runtime); err != nil {
		t.Fatal(err)
	}
	var web fs.FS = fstest.MapFS{"operations.html": &fstest.MapFile{Data: []byte("ok")}}
	events := runtime.Operations()
	return NewServer(runtime, web).Router(), string(events[0].EventID)
}

func TestCaptionEndToEnd(t *testing.T) {
	handler, eventID := captionHandler(t)
	ingest := func(language string, sequence int64, start int64, end int64) {
		t.Helper()
		body, _ := json.Marshal(map[string]any{
			"event_id": eventID, "language": language, "version": 1,
			"sequence": sequence, "start": start, "end": end, "text": "cue",
		})
		request := httptest.NewRequest(http.MethodPost, "/api/captions/cues", bytes.NewReader(body))
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusCreated {
			t.Fatalf("ingest %s/%d status = %d body = %s", language, sequence, recorder.Code, recorder.Body.String())
		}
	}
	ingest("en", 1, 1000, 1100)
	ingest("en", 2, 1100, 1200)
	ingest("fr", 1, 1000, 1100)

	reportRequest := httptest.NewRequest(http.MethodGet, "/api/captions/report?event_id="+eventID+"&reference=en&timecode=1050", nil)
	reportRecorder := httptest.NewRecorder()
	handler.ServeHTTP(reportRecorder, reportRequest)
	if reportRecorder.Code != http.StatusOK {
		t.Fatalf("report status = %d", reportRecorder.Code)
	}
	var report struct {
		ReferenceSet bool `json:"reference_set"`
		Tracks       []struct {
			Language string                  `json:"language"`
			Issues   []struct{ Kind string } `json:"issues"`
		} `json:"tracks"`
	}
	if err := json.Unmarshal(reportRecorder.Body.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if !report.ReferenceSet || len(report.Tracks) != 2 {
		t.Fatalf("report = %s", reportRecorder.Body.String())
	}
	for _, track := range report.Tracks {
		if track.Language == "fr" && len(track.Issues) != 1 {
			t.Fatalf("fr issues = %d", len(track.Issues))
		}
	}

	activeRequest := httptest.NewRequest(http.MethodGet, "/api/captions/active?event_id="+eventID+"&language=fr&timecode=1050", nil)
	activeRecorder := httptest.NewRecorder()
	handler.ServeHTTP(activeRecorder, activeRequest)
	if activeRecorder.Code != http.StatusOK {
		t.Fatalf("active status = %d body = %s", activeRecorder.Code, activeRecorder.Body.String())
	}
	var active struct {
		Cues []struct{ Sequence int64 } `json:"cues"`
	}
	if err := json.Unmarshal(activeRecorder.Body.Bytes(), &active); err != nil {
		t.Fatal(err)
	}
	if len(active.Cues) != 1 || active.Cues[0].Sequence != 1 {
		t.Fatalf("active cues = %+v", active.Cues)
	}

	tracksRequest := httptest.NewRequest(http.MethodGet, "/api/captions/tracks?event_id="+eventID, nil)
	tracksRecorder := httptest.NewRecorder()
	handler.ServeHTTP(tracksRecorder, tracksRequest)
	if tracksRecorder.Code != http.StatusOK {
		t.Fatalf("tracks status = %d", tracksRecorder.Code)
	}

	for _, endpoint := range []string{
		"/api/captions/tracks",
		"/api/captions/report",
		"/api/captions/active?language=en",
	} {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, endpoint, nil))
		if recorder.Code != http.StatusBadRequest {
			t.Fatalf("%s without event_id status = %d", endpoint, recorder.Code)
		}
	}
}
