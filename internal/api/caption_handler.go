package api

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/wyw14/solo-035/internal/service"
)

func (server *Server) ingestCaptionCue(writer http.ResponseWriter, request *http.Request) {
	defer request.Body.Close()
	var input service.CaptionCueInput
	decoder := json.NewDecoder(http.MaxBytesReader(writer, request.Body, 64*1024))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid caption cue payload")
		return
	}
	cue, err := server.runtime.IngestCaptionCue(input)
	if err != nil {
		writeError(writer, http.StatusConflict, err.Error())
		return
	}
	writeJSON(writer, http.StatusCreated, cue)
}

func (server *Server) captionTracks(writer http.ResponseWriter, request *http.Request) {
	eventID := request.URL.Query().Get("event_id")
	if eventID == "" {
		writeError(writer, http.StatusBadRequest, "event_id is required")
		return
	}
	tracks, err := server.runtime.CaptionTracks(eventID)
	if err != nil {
		writeError(writer, http.StatusConflict, err.Error())
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"tracks": tracks})
}

func (server *Server) captionReport(writer http.ResponseWriter, request *http.Request) {
	query := request.URL.Query()
	eventID := query.Get("event_id")
	if eventID == "" {
		writeError(writer, http.StatusBadRequest, "event_id is required")
		return
	}
	timecode, err := optionalTimecode(query.Get("timecode"))
	if err != nil {
		writeError(writer, http.StatusBadRequest, err.Error())
		return
	}
	report, err := server.runtime.CaptionReport(eventID, query.Get("reference"), timecode)
	if err != nil {
		writeError(writer, http.StatusConflict, err.Error())
		return
	}
	writeJSON(writer, http.StatusOK, report)
}

func (server *Server) activeCaptions(writer http.ResponseWriter, request *http.Request) {
	query := request.URL.Query()
	eventID := query.Get("event_id")
	language := query.Get("language")
	if eventID == "" || language == "" {
		writeError(writer, http.StatusBadRequest, "event_id and language are required")
		return
	}
	timecode, err := optionalTimecode(query.Get("timecode"))
	if err != nil {
		writeError(writer, http.StatusBadRequest, err.Error())
		return
	}
	active, err := server.runtime.ActiveCaptions(eventID, language, timecode)
	if err != nil {
		writeError(writer, http.StatusConflict, err.Error())
		return
	}
	writeJSON(writer, http.StatusOK, active)
}

func optionalTimecode(raw string) (*int64, error) {
	if raw == "" {
		return nil, nil
	}
	timecode, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return nil, err
	}
	return &timecode, nil
}
