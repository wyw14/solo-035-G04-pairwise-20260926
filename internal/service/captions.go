package service

import (
	"fmt"

	"github.com/wyw14/solo-035/internal/caption"
	"github.com/wyw14/solo-035/internal/model"
)

// CaptionCueInput is one human-supplied translation cue. Start and End are
// source timecodes; Version is the translator's own cue version. The cue is
// pinned to the event's current program revision at ingest time.
type CaptionCueInput struct {
	EventID  string `json:"event_id"`
	Language string `json:"language"`
	Version  uint64 `json:"version"`
	Sequence int64  `json:"sequence"`
	Start    int64  `json:"start"`
	End      int64  `json:"end"`
	Text     string `json:"text"`
}

// ActiveCaptionView answers the broadcast query: which cues of one language
// are on air right now.
type ActiveCaptionView struct {
	EventID  model.EventID         `json:"event_id"`
	Language string                `json:"language"`
	Revision model.RevisionID      `json:"revision"`
	Timecode int64                 `json:"timecode"`
	Reviewed bool                  `json:"reviewed"`
	Cues     []caption.SubtitleCue `json:"cues"`
}

// eventTimecode is the event's latest observed source timecode, used as the
// broadcast clock for caption checks and queries.
func (runtime *Runtime) eventTimecode(eventID model.EventID) int64 {
	if status, ok := runtime.SyncState.Get(eventID); ok {
		return status.LastTimecode
	}
	return 0
}

// IngestCaptionCue accepts one human translation for the event's current
// program revision.
func (runtime *Runtime) IngestCaptionCue(input CaptionCueInput) (caption.SubtitleCue, error) {
	event, ok := runtime.Events.Get(model.EventID(input.EventID))
	if !ok {
		return caption.SubtitleCue{}, fmt.Errorf("event %s not found", input.EventID)
	}
	return runtime.CaptionLibrary.Ingest(caption.SubtitleCue{
		EventID:  event.ID,
		Language: input.Language,
		Revision: event.Revision,
		Version:  input.Version,
		Sequence: input.Sequence,
		Start:    input.Start,
		End:      input.End,
		Text:     input.Text,
	})
}

// CaptionTracks summarizes every language track of an event against its
// current program revision.
func (runtime *Runtime) CaptionTracks(eventID string) ([]caption.TrackView, error) {
	event, ok := runtime.Events.Get(model.EventID(eventID))
	if !ok {
		return nil, fmt.Errorf("event %s not found", eventID)
	}
	return runtime.CaptionLibrary.Tracks(event.ID, event.Revision), nil
}

// CaptionReport audits an event's language tracks for missing translations,
// overlaps, and expired cues. A non-empty reference language is remembered as
// the alignment source. The check runs at the given timecode, or at the
// event's latest observed timecode when none is given.
func (runtime *Runtime) CaptionReport(eventID string, reference string, timecode *int64) (caption.Report, error) {
	event, ok := runtime.Events.Get(model.EventID(eventID))
	if !ok {
		return caption.Report{}, fmt.Errorf("event %s not found", eventID)
	}
	if reference != "" {
		if err := runtime.CaptionLibrary.SetReference(event.ID, reference); err != nil {
			return caption.Report{}, err
		}
	}
	at := runtime.eventTimecode(event.ID)
	if timecode != nil {
		at = *timecode
	}
	return runtime.CaptionLibrary.Report(event.ID, event.Revision, at), nil
}

// ReviewCaptionTrack signs a language track off for the event's current
// program revision, clearing it from the re-review list.
func (runtime *Runtime) ReviewCaptionTrack(eventID string, language string) (caption.TrackReview, error) {
	event, ok := runtime.Events.Get(model.EventID(eventID))
	if !ok {
		return caption.TrackReview{}, fmt.Errorf("event %s not found", eventID)
	}
	return runtime.CaptionLibrary.MarkReviewed(event.ID, language, event.Revision)
}

// ActiveCaptions returns the cues of one language that are on air at the
// given timecode, or at the event's latest observed timecode when none is
// given.
func (runtime *Runtime) ActiveCaptions(eventID string, language string, timecode *int64) (ActiveCaptionView, error) {
	event, ok := runtime.Events.Get(model.EventID(eventID))
	if !ok {
		return ActiveCaptionView{}, fmt.Errorf("event %s not found", eventID)
	}
	at := runtime.eventTimecode(event.ID)
	if timecode != nil {
		at = *timecode
	}
	cues, err := runtime.CaptionLibrary.Active(event.ID, language, event.Revision, at)
	if err != nil {
		return ActiveCaptionView{}, err
	}
	return ActiveCaptionView{
		EventID:  event.ID,
		Language: caption.NormalizeLanguage(language),
		Revision: event.Revision,
		Timecode: at,
		Reviewed: runtime.CaptionLibrary.Reviewed(event.ID, language, event.Revision),
		Cues:     cues,
	}, nil
}
