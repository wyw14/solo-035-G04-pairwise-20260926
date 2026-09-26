package service

import (
	"fmt"
	"sort"

	"github.com/google/uuid"
	"github.com/wyw14/solo-035/internal/model"
	"github.com/wyw14/solo-035/internal/subtitle"
)

// SubtitleTrackView is one language track with its current audit status.
type SubtitleTrackView struct {
	EventID        model.EventID     `json:"event_id"`
	Language       subtitle.Language `json:"language"`
	Revision       model.RevisionID  `json:"revision"`
	CueCount       int               `json:"cue_count"`
	CurrentCues    int               `json:"current_cues"`
	ReviewedCues   int               `json:"reviewed_cues"`
	ReReview       bool              `json:"re_review_pending"`
	RegisteredWith subtitle.Language `json:"reference_language,omitempty"`
}

// RegisterSubtitleLanguage opens a language track on a live event. The
// first language registered for an event is treated as the reference
// (original-language) timeline for coverage audits.
func (runtime *Runtime) RegisterSubtitleLanguage(eventID model.EventID, language string) (subtitle.Track, error) {
	event, ok := runtime.Events.Get(eventID)
	if !ok {
		return subtitle.Track{}, fmt.Errorf("event %s not found", eventID)
	}
	track, err := runtime.SubtitleFlow.RegisterTrack(eventID, subtitle.Language(language), event.Revision)
	if err != nil {
		return subtitle.Track{}, err
	}
	return track, nil
}

// SubmitSubtitleCue ingests one human translation. When cueID or revision
// are blank they are generated or defaulted to the event's current program
// revision respectively.
func (runtime *Runtime) SubmitSubtitleCue(eventID model.EventID, language, cueID, text string, startMS, endMS int64, revision model.RevisionID) (subtitle.Cue, error) {
	event, ok := runtime.Events.Get(eventID)
	if !ok {
		return subtitle.Cue{}, fmt.Errorf("event %s not found", eventID)
	}
	if cueID == "" {
		cueID = uuid.NewString()
	}
	if !revision.Valid() {
		revision = event.Revision
	}
	// The first cue for a language implicitly opens its track; explicit
	// registration stays available for pre-creating the language list.
	if _, err := runtime.SubtitleFlow.RegisterTrack(eventID, subtitle.Language(language), event.Revision); err != nil {
		return subtitle.Cue{}, err
	}
	cue := subtitle.Cue{
		ID:       cueID,
		Language: subtitle.Language(language),
		Revision: revision,
		Text:     text,
		Start:    startMS,
		End:      endMS,
	}
	return runtime.SubtitleFlow.SubmitCue(eventID, cue)
}

// ReviewSubtitleCue records the human review decision for one cue.
func (runtime *Runtime) ReviewSubtitleCue(eventID model.EventID, language, cueID string) (subtitle.Cue, error) {
	return runtime.SubtitleFlow.ReviewCue(eventID, subtitle.Language(language), cueID)
}

// SubtitleAudit runs the live-event checks. When referenceLanguage is blank
// the earliest-registered track defines the coverage timeline; when no
// tracks exist, coverage checking is skipped.
func (runtime *Runtime) SubtitleAudit(eventID model.EventID, referenceLanguage string) (subtitle.AuditReport, error) {
	event, ok := runtime.Events.Get(eventID)
	if !ok {
		return subtitle.AuditReport{}, fmt.Errorf("event %s not found", eventID)
	}
	var reference []subtitle.Interval
	if referenceLanguage != "" {
		derived, err := runtime.SubtitleFlow.AuditWithReference(eventID, event.Revision, subtitle.Language(referenceLanguage))
		if err != nil {
			return subtitle.AuditReport{}, err
		}
		return derived, nil
	}
	if language, ok := runtime.referenceLanguage(eventID); ok {
		reference = runtime.referenceIntervals(eventID, language)
	}
	return runtime.SubtitleFlow.Audit(eventID, event.Revision, reference), nil
}

// SubtitleReReviewQueue lists language tracks that must be re-audited
// because the program was revised after translation.
func (runtime *Runtime) SubtitleReReviewQueue() []subtitle.ReReviewItem {
	return runtime.SubtitleFlow.ReReviewQueue()
}

// ActiveSubtitle answers the on-air query: the valid cue of the selected
// language at a given event timecode. The boolean is false when no cue is
// active (an empty interval), distinct from an error.
func (runtime *Runtime) ActiveSubtitle(eventID model.EventID, language string, timecodeMS int64) (subtitle.PlaybackCue, bool, error) {
	cue, err := runtime.SubtitleFlow.ActiveAt(eventID, subtitle.Language(language), timecodeMS)
	if err != nil {
		return subtitle.PlaybackCue{}, false, err
	}
	return cue, cue.Text != "", nil
}

// SubtitleWindow returns valid cues intersecting a playback timecode window.
func (runtime *Runtime) SubtitleWindow(eventID model.EventID, language string, fromMS, toMS int64) ([]subtitle.PlaybackCue, error) {
	return runtime.SubtitleFlow.Window(eventID, subtitle.Language(language), fromMS, toMS)
}

// SubtitleTrackViews summarizes the tracks of one event for the console.
func (runtime *Runtime) SubtitleTrackViews(eventID model.EventID) []SubtitleTrackView {
	event, ok := runtime.Events.Get(eventID)
	tracks := runtime.SubtitleFlow.Tracks(eventID)
	reference, _ := runtime.referenceLanguage(eventID)
	views := make([]SubtitleTrackView, 0, len(tracks))
	for _, track := range tracks {
		view := SubtitleTrackView{
			EventID:  eventID,
			Language: track.Language,
			CueCount: len(track.Cues),
			ReReview: track.ReReviewPending,
		}
		if ok {
			view.Revision = event.Revision
		}
		for _, cue := range track.Cues {
			if ok && cue.Revision == event.Revision {
				view.CurrentCues++
			}
			if cue.Reviewed {
				view.ReviewedCues++
			}
		}
		if track.Language == reference {
			view.RegisteredWith = reference
		}
		views = append(views, view)
	}
	sort.Slice(views, func(i, j int) bool { return views[i].Language < views[j].Language })
	return views
}

// AllSubtitleTrackViews summarizes tracks across every event.
func (runtime *Runtime) AllSubtitleTrackViews() []SubtitleTrackView {
	views := make([]SubtitleTrackView, 0)
	for _, event := range runtime.Events.List() {
		views = append(views, runtime.SubtitleTrackViews(event.ID)...)
	}
	return views
}

// referenceLanguage picks the earliest-registered track of an event as the
// original-language timeline.
func (runtime *Runtime) referenceLanguage(eventID model.EventID) (subtitle.Language, bool) {
	tracks := runtime.SubtitleFlow.Tracks(eventID)
	if len(tracks) == 0 {
		return "", false
	}
	reference := tracks[0]
	for _, track := range tracks[1:] {
		if track.RegisteredAt.Before(reference.RegisteredAt) {
			reference = track
		}
	}
	return reference.Language, true
}

func (runtime *Runtime) referenceIntervals(eventID model.EventID, language subtitle.Language) []subtitle.Interval {
	track, ok := runtime.Subtitles.Track(eventID, language)
	if !ok {
		return nil
	}
	event, ok := runtime.Events.Get(eventID)
	if !ok {
		return nil
	}
	intervals := make([]subtitle.Interval, 0, len(track.Cues))
	for _, cue := range track.Cues {
		if cue.Revision == event.Revision {
			intervals = append(intervals, subtitle.Interval{StartMS: cue.Start, EndMS: cue.End})
		}
	}
	return intervals
}
