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

func bootSubtitles(t *testing.T) (*Server, string) {
	t.Helper()
	runtime, err := service.NewRuntime(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Bootstrap(runtime); err != nil {
		t.Fatal(err)
	}
	server := NewServer(runtime, fstest.MapFS{"operations.html": &fstest.MapFile{Data: []byte("ok")}})
	operations := decodeBody[struct {
		Operations []struct {
			EventID string `json:"event_id"`
		} `json:"operations"`
	}](t, server, httptest.NewRequest(http.MethodGet, "/api/operations", nil))
	if len(operations.Operations) != 1 {
		t.Fatalf("operations = %d", len(operations.Operations))
	}
	return server, operations.Operations[0].EventID
}

func requestJSON(t *testing.T, server *Server, method, target string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var reader *bytes.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		reader = bytes.NewReader(encoded)
	} else {
		reader = bytes.NewReader(nil)
	}
	recorder := httptest.NewRecorder()
	server.Router().ServeHTTP(recorder, httptest.NewRequest(method, target, reader))
	return recorder
}

func decodeBody[T any](t *testing.T, server *Server, request *http.Request) T {
	t.Helper()
	recorder := httptest.NewRecorder()
	server.Router().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", recorder.Code, recorder.Body.String())
	}
	var value T
	if err := json.Unmarshal(recorder.Body.Bytes(), &value); err != nil {
		t.Fatal(err)
	}
	return value
}

func TestSubtitleAlignmentAuditAndPlayback(t *testing.T) {
	server, eventID := bootSubtitles(t)

	// Bootstrap seeds aligned, reviewed zh-CN and en tracks: audit is clean.
	audit := decodeBody[struct {
		HealthyUnused struct{} `json:"-"`
		Issues        []any    `json:"issues"`
		Languages     []string `json:"languages"`
	}](t, server, httptest.NewRequest(http.MethodGet, "/api/subtitle-audit?event_id="+eventID, nil))
	if len(audit.Issues) != 0 {
		t.Fatalf("seeded tracks must be aligned, issues = %s", audit.Issues)
	}

	// Playback serves the reviewed English cue at 1000ms (second cue covers 3000..6000; first 0..3000).
	playback := decodeBody[struct {
		Active bool `json:"active"`
		Cue    struct {
			Text string `json:"text"`
		} `json:"cue"`
	}](t, server, httptest.NewRequest(http.MethodGet, "/api/subtitles?event_id="+eventID+"&language=en&timecode_ms=1000", nil))
	if !playback.Active || playback.Cue.Text != "Good evening" {
		t.Fatalf("playback = %+v", playback)
	}

	// Submit an unreviewed French cue: it audits as missing coverage elsewhere
	// and unreviewed, and must not be served on air yet.
	response := requestJSON(t, server, http.MethodPost, "/api/subtitle-cues", map[string]any{
		"event_id": eventID, "language": "fr", "text": "Bonsoir", "start_ms": 0, "end_ms": 1500,
	})
	if response.Code != http.StatusCreated {
		t.Fatalf("submit status = %d body = %s", response.Code, response.Body.String())
	}
	blocked := decodeBody[struct {
		Active bool `json:"active"`
	}](t, server, httptest.NewRequest(http.MethodGet, "/api/subtitles?event_id="+eventID+"&language=fr&timecode_ms=1000", nil))
	if blocked.Active {
		t.Fatal("unreviewed cue must not be served")
	}
}

func TestSubtitleProgramRevisionListsReReviewTracks(t *testing.T) {
	server, eventID := bootSubtitles(t)

	revision := requestJSON(t, server, http.MethodPost, "/api/control", map[string]any{
		"action": "advance-program", "event_id": eventID,
	})
	if revision.Code != http.StatusOK {
		t.Fatalf("advance status = %d body = %s", revision.Code, revision.Body.String())
	}

	queue := decodeBody[struct {
		Items []struct {
			Language  string `json:"language"`
			StaleCues int    `json:"stale_cues"`
		} `json:"rereview"`
	}](t, server, httptest.NewRequest(http.MethodGet, "/api/subtitle-rereview", nil))
	if len(queue.Items) != 2 {
		t.Fatalf("rereview tracks = %d, want 2 (zh-CN, en)", len(queue.Items))
	}
	for _, item := range queue.Items {
		if item.StaleCues != 3 {
			t.Fatalf("stale cues for %s = %d, want 3", item.Language, item.StaleCues)
		}
	}

	// Stale cues no longer play out after the revision.
	playback := decodeBody[struct {
		Active bool `json:"active"`
	}](t, server, httptest.NewRequest(http.MethodGet, "/api/subtitles?event_id="+eventID+"&language=en&timecode_ms=1000", nil))
	if playback.Active {
		t.Fatal("stale cue must not play after program revision")
	}
}

func TestSubtitleCheckpointRoundTrip(t *testing.T) {
	server, eventID := bootSubtitles(t)
	checkpoint := decodeBody[service.ControlCheckpoint](t, server, httptest.NewRequest(http.MethodGet, "/api/checkpoint", nil))
	if len(checkpoint.Subtitles) != 1 || len(checkpoint.Subtitles[0].Tracks) != 2 {
		t.Fatalf("checkpoint subtitles = %+v", checkpoint.Subtitles)
	}

	fresh, err := service.NewRuntime(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var web fs.FS = fstest.MapFS{"operations.html": &fstest.MapFile{Data: []byte("ok")}}
	restored := NewServer(fresh, web)
	recorder := requestJSON(t, restored, http.MethodPost, "/api/checkpoint", checkpoint)
	if recorder.Code != http.StatusOK {
		t.Fatalf("restore status = %d body = %s", recorder.Code, recorder.Body.String())
	}
	tracks := decodeBody[struct {
		Tracks []struct {
			Language string `json:"language"`
		} `json:"tracks"`
	}](t, restored, httptest.NewRequest(http.MethodGet, "/api/subtitle-tracks?event_id="+eventID, nil))
	if len(tracks.Tracks) != 2 {
		t.Fatalf("restored tracks = %d", len(tracks.Tracks))
	}
}
