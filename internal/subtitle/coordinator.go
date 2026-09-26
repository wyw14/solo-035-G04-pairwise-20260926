package subtitle

import (
	"fmt"
	"sort"
	"time"

	"github.com/wyw14/solo-035/internal/model"
)

// Coordinator is the service-facing facade over the subtitle state: it
// ingests human translations, runs live-event audits and answers broadcast
// playback queries.
type Coordinator struct {
	state   *State
	auditor *Auditor
	clock   func() time.Time
}

func NewCoordinator(state *State, clock func() time.Time) *Coordinator {
	if clock == nil {
		clock = time.Now
	}
	return &Coordinator{state: state, auditor: NewAuditor(state, clock), clock: clock}
}

// RegisterTrack opens a language track for a live event.
func (coordinator *Coordinator) RegisterTrack(eventID model.EventID, language Language, current model.RevisionID) (Track, error) {
	track, err := coordinator.state.RegisterLanguage(eventID, language, coordinator.clock())
	if err != nil {
		return Track{}, err
	}
	coordinator.state.SetCurrentRevision(eventID, current)
	return track, nil
}

// SubmitCue validates and stores one human-provided translation carrying its
// start/end timecodes and the program version it was translated against.
func (coordinator *Coordinator) SubmitCue(eventID model.EventID, cue Cue) (Cue, error) {
	if cue.ReceivedAt.IsZero() {
		cue.ReceivedAt = coordinator.clock()
	}
	if err := coordinator.state.Submit(eventID, cue); err != nil {
		return Cue{}, err
	}
	return cue, nil
}

// ReviewCue records a human review pass for a cue.
func (coordinator *Coordinator) ReviewCue(eventID model.EventID, language Language, cueID string) (Cue, error) {
	return coordinator.state.MarkReviewed(eventID, language, cueID, coordinator.clock())
}

// ProgramRevised marks a new program revision and lists the language tracks
// whose assets are now stale and must be re-audited.
func (coordinator *Coordinator) ProgramRevised(eventID model.EventID, revision model.RevisionID) []ReReviewItem {
	return coordinator.state.AdvanceRevision(eventID, revision, coordinator.clock())
}

// ReReviewQueue lists every track awaiting re-review across all events.
func (coordinator *Coordinator) ReReviewQueue() []ReReviewItem {
	return coordinator.state.ReReviewPending()
}

// Audit runs the live-event checks for one event against a reference
// timeline (nil skips coverage checking).
func (coordinator *Coordinator) Audit(eventID model.EventID, current model.RevisionID, reference []Interval) AuditReport {
	return coordinator.auditor.AuditEvent(eventID, current, reference)
}

// AuditWithReference derives the coverage timeline from the cues of a
// reference language (usually the original language) and audits every other
// track against it.
func (coordinator *Coordinator) AuditWithReference(eventID model.EventID, current model.RevisionID, referenceLanguage Language) (AuditReport, error) {
	reference, err := coordinator.auditor.ReferenceFromTrack(eventID, referenceLanguage, current)
	if err != nil {
		return AuditReport{}, err
	}
	return coordinator.auditor.AuditEvent(eventID, current, reference), nil
}

// ActiveAt returns the current-revision, reviewed cue of one language whose
// interval contains the given event timecode. This is the broadcast query
// used while the international signal is on air. Unreviewed or stale cues
// are never served.
func (coordinator *Coordinator) ActiveAt(eventID model.EventID, language Language, timecodeMS int64) (PlaybackCue, error) {
	current, ok := coordinator.state.CurrentRevision(eventID)
	if !ok {
		return PlaybackCue{}, fmt.Errorf("event %s has no subtitle program", eventID)
	}
	track, ok := coordinator.state.Track(eventID, language)
	if !ok {
		return PlaybackCue{}, fmt.Errorf("language %s is not registered for event %s", language, eventID)
	}
	candidates := make([]Cue, 0, 1)
	for _, cue := range track.Cues {
		if cue.Revision != current || !cue.Reviewed {
			continue
		}
		if cue.Start <= timecodeMS && timecodeMS < cue.End {
			candidates = append(candidates, cue)
		}
	}
	if len(candidates) == 0 {
		return PlaybackCue{}, nil
	}
	sort.SliceStable(candidates, func(i, j int) bool { return candidates[i].Start > candidates[j].Start })
	active := candidates[0]
	return PlaybackCue{Language: language, Text: active.Text, StartMS: active.Start, EndMS: active.End}, nil
}

// Window returns every valid cue of one language intersecting a playback
// window, sorted by start timecode — useful for scrubbing or prefetching.
func (coordinator *Coordinator) Window(eventID model.EventID, language Language, fromMS, toMS int64) ([]PlaybackCue, error) {
	current, ok := coordinator.state.CurrentRevision(eventID)
	if !ok {
		return nil, fmt.Errorf("event %s has no subtitle program", eventID)
	}
	track, ok := coordinator.state.Track(eventID, language)
	if !ok {
		return nil, fmt.Errorf("language %s is not registered for event %s", language, eventID)
	}
	cues := make([]PlaybackCue, 0)
	for _, cue := range track.Cues {
		if cue.Revision != current || !cue.Reviewed {
			continue
		}
		if cue.Start < toMS && fromMS < cue.End {
			cues = append(cues, PlaybackCue{Language: language, Text: cue.Text, StartMS: cue.Start, EndMS: cue.End})
		}
	}
	sort.SliceStable(cues, func(i, j int) bool { return cues[i].StartMS < cues[j].StartMS })
	return cues, nil
}

// Tracks exposes the tracks of one event for status/display purposes.
func (coordinator *Coordinator) Tracks(eventID model.EventID) []Track {
	return coordinator.state.TracksFor(eventID)
}

// Languages exposes the registered languages of one event.
func (coordinator *Coordinator) Languages(eventID model.EventID) []Language {
	return coordinator.state.LanguagesFor(eventID)
}
