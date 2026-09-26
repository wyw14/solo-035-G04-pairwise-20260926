package api

import (
	"net/http"
	"strconv"

	"github.com/wyw14/solo-035/internal/model"
)

// subtitleTracks lists every language track (or one event's when event_id is
// supplied).
func (server *Server) subtitleTracks(writer http.ResponseWriter, request *http.Request) {
	eventID := model.EventID(request.URL.Query().Get("event_id"))
	if eventID != "" {
		if !eventID.Valid() {
			writeError(writer, http.StatusBadRequest, "event_id is invalid")
			return
		}
		writeJSON(writer, http.StatusOK, map[string]any{"tracks": server.runtime.SubtitleTrackViews(eventID)})
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"tracks": server.runtime.AllSubtitleTrackViews()})
}

// subtitleAudit runs the live-event missing/overlap/stale/unreviewed checks.
func (server *Server) subtitleAudit(writer http.ResponseWriter, request *http.Request) {
	eventID := model.EventID(request.URL.Query().Get("event_id"))
	if !eventID.Valid() {
		writeError(writer, http.StatusBadRequest, "event_id is required and must be a valid identity")
		return
	}
	report, err := server.runtime.SubtitleAudit(eventID, request.URL.Query().Get("language"))
	if err != nil {
		writeError(writer, http.StatusConflict, err.Error())
		return
	}
	writeJSON(writer, http.StatusOK, report)
}

// subtitleRereview lists tracks that must be re-audited after a program
// revision.
func (server *Server) subtitleRereview(writer http.ResponseWriter, request *http.Request) {
	writeJSON(writer, http.StatusOK, map[string]any{"rereview": server.runtime.SubtitleReReviewQueue()})
}

// subtitles answers the on-air playback query for one selected language.
// With only timecode_ms supplied it returns the active cue (if any); adding
// to_ms returns every valid cue intersecting [timecode_ms, to_ms).
func (server *Server) subtitles(writer http.ResponseWriter, request *http.Request) {
	query := request.URL.Query()
	eventID := model.EventID(query.Get("event_id"))
	language := query.Get("language")
	if !eventID.Valid() {
		writeError(writer, http.StatusBadRequest, "event_id is required and must be a valid identity")
		return
	}
	if language == "" {
		writeError(writer, http.StatusBadRequest, "language is required")
		return
	}
	fromMS, err := strconv.ParseInt(query.Get("timecode_ms"), 10, 64)
	if err != nil {
		writeError(writer, http.StatusBadRequest, "timecode_ms is required")
		return
	}
	if raw := query.Get("to_ms"); raw != "" {
		toMS, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || toMS <= fromMS {
			writeError(writer, http.StatusBadRequest, "to_ms must be greater than timecode_ms")
			return
		}
		cues, err := server.runtime.SubtitleWindow(eventID, language, fromMS, toMS)
		if err != nil {
			writeError(writer, http.StatusConflict, err.Error())
			return
		}
		writeJSON(writer, http.StatusOK, map[string]any{"cues": cues})
		return
	}
	cue, active, err := server.runtime.ActiveSubtitle(eventID, language, fromMS)
	if err != nil {
		writeError(writer, http.StatusConflict, err.Error())
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"active": active, "cue": cue})
}
