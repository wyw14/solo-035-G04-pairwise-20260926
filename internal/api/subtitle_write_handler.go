package api

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/wyw14/solo-035/internal/model"
)

type registerSubtitleTrackRequest struct {
	EventID  string `json:"event_id"`
	Language string `json:"language"`
}

func (server *Server) registerSubtitleTrack(writer http.ResponseWriter, request *http.Request) {
	input, ok := decodeSubtitleRequest[registerSubtitleTrackRequest](writer, request)
	if !ok {
		return
	}
	input.Language = strings.TrimSpace(input.Language)
	if !model.EventID(input.EventID).Valid() {
		writeError(writer, http.StatusBadRequest, "event_id is required and must be a valid identity")
		return
	}
	if input.Language == "" {
		writeError(writer, http.StatusBadRequest, "language is required")
		return
	}
	track, err := server.runtime.RegisterSubtitleLanguage(model.EventID(input.EventID), input.Language)
	if err != nil {
		writeError(writer, http.StatusConflict, err.Error())
		return
	}
	writeJSON(writer, http.StatusCreated, track)
}

type submitSubtitleCueRequest struct {
	EventID  string `json:"event_id"`
	Language string `json:"language"`
	CueID    string `json:"cue_id"`
	Text     string `json:"text"`
	StartMS  int64  `json:"start_ms"`
	EndMS    int64  `json:"end_ms"`
	Revision string `json:"revision"`
}

func (server *Server) submitSubtitleCue(writer http.ResponseWriter, request *http.Request) {
	input, ok := decodeSubtitleRequest[submitSubtitleCueRequest](writer, request)
	if !ok {
		return
	}
	input.Language = strings.TrimSpace(input.Language)
	input.Text = strings.TrimSpace(input.Text)
	if !model.EventID(input.EventID).Valid() {
		writeError(writer, http.StatusBadRequest, "event_id is required and must be a valid identity")
		return
	}
	if input.Language == "" {
		writeError(writer, http.StatusBadRequest, "language is required")
		return
	}
	if input.Text == "" {
		writeError(writer, http.StatusBadRequest, "text is required")
		return
	}
	if input.StartMS < 0 || input.EndMS <= input.StartMS {
		writeError(writer, http.StatusBadRequest, "end_ms must be greater than start_ms")
		return
	}
	cue, err := server.runtime.SubmitSubtitleCue(
		model.EventID(input.EventID), input.Language, input.CueID, input.Text,
		input.StartMS, input.EndMS, model.RevisionID(input.Revision),
	)
	if err != nil {
		writeError(writer, http.StatusConflict, err.Error())
		return
	}
	writeJSON(writer, http.StatusCreated, cue)
}

type reviewSubtitleCueRequest struct {
	EventID  string `json:"event_id"`
	Language string `json:"language"`
	CueID    string `json:"cue_id"`
}

func (server *Server) reviewSubtitleCue(writer http.ResponseWriter, request *http.Request) {
	input, ok := decodeSubtitleRequest[reviewSubtitleCueRequest](writer, request)
	if !ok {
		return
	}
	if !model.EventID(input.EventID).Valid() || input.Language == "" || input.CueID == "" {
		writeError(writer, http.StatusBadRequest, "event_id, language and cue_id are required")
		return
	}
	cue, err := server.runtime.ReviewSubtitleCue(model.EventID(input.EventID), input.Language, input.CueID)
	if err != nil {
		writeError(writer, http.StatusConflict, err.Error())
		return
	}
	writeJSON(writer, http.StatusOK, cue)
}

func decodeSubtitleRequest[T any](writer http.ResponseWriter, request *http.Request) (T, bool) {
	defer request.Body.Close()
	var input T
	decoder := json.NewDecoder(http.MaxBytesReader(writer, request.Body, 128*1024))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid subtitle payload")
		return input, false
	}
	return input, true
}
