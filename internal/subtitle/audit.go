package subtitle

import (
	"fmt"
	"sort"
	"time"

	"github.com/wyw14/solo-035/internal/model"
)

// Interval is a half-open timecode span [StartMS, EndMS) in event time.
type Interval struct {
	StartMS int64 `json:"start_ms"`
	EndMS   int64 `json:"end_ms"`
}

// Auditor runs the per-live-event checks: missing translations against a
// reference timeline, overlapping cues on a track, stale-revision cues and
// cues awaiting human review.
type Auditor struct {
	state *State
	clock func() time.Time
}

func NewAuditor(state *State, clock func() time.Time) *Auditor {
	return &Auditor{state: state, clock: clock}
}

// AuditEvent checks every registered language track of one event. The
// reference timeline is optional: when non-empty, each track must cover it
// completely with current-revision cues. A zero result means the
// international signal is fully subtitled in every registered language.
func (auditor *Auditor) AuditEvent(eventID model.EventID, current model.RevisionID, reference []Interval) AuditReport {
	now := auditor.clock().UTC()
	tracks := auditor.state.TracksFor(eventID)
	languages := make([]Language, 0, len(tracks))
	issues := make([]Issue, 0)
	covered := normalizeIntervals(reference)
	for _, track := range tracks {
		languages = append(languages, track.Language)
		issues = append(issues, auditTrack(eventID, current, covered, track)...)
	}
	sort.Slice(issues, func(i, j int) bool {
		if issues[i].Language != issues[j].Language {
			return issues[i].Language < issues[j].Language
		}
		if issues[i].Kind != issues[j].Kind {
			return issues[i].Kind < issues[j].Kind
		}
		return issues[i].StartMS < issues[j].StartMS
	})
	return AuditReport{
		EventID:         eventID,
		CurrentRevision: current,
		Languages:       languages,
		Issues:          issues,
		CheckedAt:       now,
	}
}

// ReferenceFromTrack builds a coverage timeline from the current-revision
// cues of one language (typically the source/original language). It lets a
// single human-authored track define what every other language must cover.
func (auditor *Auditor) ReferenceFromTrack(eventID model.EventID, language Language, current model.RevisionID) ([]Interval, error) {
	track, ok := auditor.state.Track(eventID, language)
	if !ok {
		return nil, fmt.Errorf("language %s is not registered for event %s", language, eventID)
	}
	intervals := make([]Interval, 0, len(track.Cues))
	for _, cue := range track.Cues {
		if cue.Revision == current {
			intervals = append(intervals, Interval{StartMS: cue.Start, EndMS: cue.End})
		}
	}
	return normalizeIntervals(intervals), nil
}

func auditTrack(eventID model.EventID, current model.RevisionID, reference []Interval, track Track) []Issue {
	issues := make([]Issue, 0)
	currentCues := make([]Cue, 0)
	for _, cue := range track.Cues {
		if cue.Revision != current {
			issues = append(issues, Issue{
				EventID:  eventID,
				Language: track.Language,
				Kind:     IssueStale,
				Revision: cue.Revision,
				CueID:    cue.ID,
				StartMS:  cue.Start,
				EndMS:    cue.End,
				Detail:   fmt.Sprintf("cue %s is pinned to superseded revision %s", cue.ID, cue.Revision),
			})
			continue
		}
		currentCues = append(currentCues, cue)
		if !cue.Reviewed {
			issues = append(issues, Issue{
				EventID:  eventID,
				Language: track.Language,
				Kind:     IssueUnreviewed,
				Revision: cue.Revision,
				CueID:    cue.ID,
				StartMS:  cue.Start,
				EndMS:    cue.End,
				Detail:   fmt.Sprintf("cue %s has not passed human review", cue.ID),
			})
		}
	}
	issues = append(issues, overlapIssues(eventID, track.Language, current, currentCues)...)
	issues = append(issues, missingIssues(eventID, track.Language, reference, currentCues)...)
	return issues
}

func overlapIssues(eventID model.EventID, language Language, current model.RevisionID, cues []Cue) []Issue {
	ordered := append([]Cue(nil), cues...)
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].Start != ordered[j].Start {
			return ordered[i].Start < ordered[j].Start
		}
		return ordered[i].End < ordered[j].End
	})
	issues := make([]Issue, 0)
	var last *Cue
	for index := range ordered {
		cue := &ordered[index]
		if last != nil && cue.Start < last.End {
			issues = append(issues, Issue{
				EventID:  eventID,
				Language: language,
				Kind:     IssueOverlap,
				Revision: current,
				CueID:    cue.ID,
				StartMS:  cue.Start,
				EndMS:    minInt64(cue.End, last.End),
				Detail:   fmt.Sprintf("cue %s overlaps cue %s at %dms", cue.ID, last.ID, cue.Start),
			})
		}
		if last == nil || cue.End > last.End {
			last = cue
		}
	}
	return issues
}

func missingIssues(eventID model.EventID, language Language, reference []Interval, cues []Cue) []Issue {
	if len(reference) == 0 {
		return nil
	}
	coverage := normalizeIntervals(makeIntervals(cues))
	issues := make([]Issue, 0)
	for _, gap := range subtractIntervals(reference, coverage) {
		issues = append(issues, Issue{
			EventID:  eventID,
			Language: language,
			Kind:     IssueMissing,
			StartMS:  gap.StartMS,
			EndMS:    gap.EndMS,
			Detail:   fmt.Sprintf("no %s subtitle covers %d..%dms", language, gap.StartMS, gap.EndMS),
		})
	}
	return issues
}

func makeIntervals(cues []Cue) []Interval {
	intervals := make([]Interval, 0, len(cues))
	for _, cue := range cues {
		intervals = append(intervals, Interval{StartMS: cue.Start, EndMS: cue.End})
	}
	return intervals
}

// normalizeIntervals sorts intervals and merges overlapping or touching
// spans into a minimal disjoint slice.
func normalizeIntervals(intervals []Interval) []Interval {
	if len(intervals) == 0 {
		return nil
	}
	ordered := append([]Interval(nil), intervals...)
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].StartMS != ordered[j].StartMS {
			return ordered[i].StartMS < ordered[j].StartMS
		}
		return ordered[i].EndMS < ordered[j].EndMS
	})
	merged := []Interval{ordered[0]}
	for _, interval := range ordered[1:] {
		tail := &merged[len(merged)-1]
		if interval.StartMS <= tail.EndMS && interval.EndMS > tail.EndMS {
			tail.EndMS = interval.EndMS
		} else if interval.StartMS > tail.EndMS {
			merged = append(merged, interval)
		}
	}
	return merged
}

// subtractIntervals returns the portions of wanted that are not covered by
// have; both inputs are expected to be normalized disjoint intervals.
func subtractIntervals(wanted, have []Interval) []Interval {
	gaps := make([]Interval, 0)
	for _, window := range wanted {
		cursor := window.StartMS
		for _, covered := range have {
			if covered.EndMS <= cursor {
				continue
			}
			if covered.StartMS >= window.EndMS {
				break
			}
			if covered.StartMS > cursor {
				gaps = append(gaps, Interval{StartMS: cursor, EndMS: minInt64(covered.StartMS, window.EndMS)})
			}
			if covered.EndMS > cursor {
				cursor = covered.EndMS
			}
			if cursor >= window.EndMS {
				break
			}
		}
		if cursor < window.EndMS {
			gaps = append(gaps, Interval{StartMS: cursor, EndMS: window.EndMS})
		}
	}
	return normalizeIntervals(gaps)
}

func minInt64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}
