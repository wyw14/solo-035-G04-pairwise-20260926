package subtitle

import (
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/wyw14/solo-035/internal/model"
)

// State owns the subtitle assets and language tracks for every live event.
type State struct {
	mu sync.RWMutex
	// tracks is keyed by event id, then BCP-47 language tag.
	tracks map[model.EventID]map[Language]*Track
	// current remembers the program revision most recently announced via
	// AdvanceRevision. The event state remains authoritative; this lets the
	// subtitle package answer re-review and playback questions on its own.
	current map[model.EventID]model.RevisionID
}

func NewState() *State {
	return &State{
		tracks:  make(map[model.EventID]map[Language]*Track),
		current: make(map[model.EventID]model.RevisionID),
	}
}

// RegisterLanguage opens a translation track for a language on an event.
// Registering the same language more than once is idempotent.
func (state *State) RegisterLanguage(eventID model.EventID, language Language, now time.Time) (Track, error) {
	if !eventID.Valid() {
		return Track{}, fmt.Errorf("event identity is invalid")
	}
	if !language.Valid() {
		return Track{}, fmt.Errorf("language tag is required")
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	languages := state.tracks[eventID]
	if languages == nil {
		languages = make(map[Language]*Track)
		state.tracks[eventID] = languages
	}
	if track, ok := languages[language]; ok {
		return track.copy(), nil
	}
	track := &Track{EventID: eventID, Language: language, RegisteredAt: now.UTC()}
	languages[language] = track
	return track.copy(), nil
}

// Submit accepts one human-translated cue onto its language track. Cues are
// kept sorted by start timecode within each revision so audit and playback
// are linear scans. Duplicate cue identities are rejected.
func (state *State) Submit(eventID model.EventID, cue Cue) error {
	if !eventID.Valid() {
		return fmt.Errorf("event identity is invalid")
	}
	if err := cue.Valid(); err != nil {
		return err
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	track, err := state.lockedTrack(eventID, cue.Language)
	if err != nil {
		return err
	}
	for _, existing := range track.Cues {
		if existing.ID == cue.ID {
			return fmt.Errorf("subtitle cue %s was already submitted", cue.ID)
		}
	}
	cue.ReceivedAt = cue.ReceivedAt.UTC()
	track.Cues = append(track.Cues, cue)
	sort.SliceStable(track.Cues, func(i, j int) bool {
		if track.Cues[i].Revision != track.Cues[j].Revision {
			return track.Cues[i].Revision < track.Cues[j].Revision
		}
		if track.Cues[i].Start != track.Cues[j].Start {
			return track.Cues[i].Start < track.Cues[j].Start
		}
		return track.Cues[i].ID < track.Cues[j].ID
	})
	return nil
}

// MarkReviewed records the human review decision for one cue. Reviewing a
// cue that matches the current program revision clears the track's
// re-review flag.
func (state *State) MarkReviewed(eventID model.EventID, language Language, cueID string, now time.Time) (Cue, error) {
	state.mu.Lock()
	defer state.mu.Unlock()
	track, err := state.lockedTrack(eventID, language)
	if err != nil {
		return Cue{}, err
	}
	for index := range track.Cues {
		if track.Cues[index].ID != cueID {
			continue
		}
		stamped := now.UTC()
		track.Cues[index].Reviewed = true
		track.Cues[index].ReviewedAt = &stamped
		// A track leaves the re-review queue once the complete set of cues
		// translated against the current revision has passed human review.
		current := state.current[eventID]
		currentCount, currentReviewed := 0, 0
		for _, other := range track.Cues {
			if other.Revision == current {
				currentCount++
				if other.Reviewed {
					currentReviewed++
				}
			}
		}
		if currentCount > 0 && currentCount == currentReviewed {
			track.ReReviewPending = false
			track.ReReviewSince = nil
		}
		return track.Cues[index], nil
	}
	return Cue{}, fmt.Errorf("subtitle cue %s not found on %s/%s", cueID, eventID, language)
}

// AdvanceRevision announces a new program revision. Every track that still
// references the previous revision is flagged for re-review; the stale cues
// are retained as history rather than deleted.
func (state *State) AdvanceRevision(eventID model.EventID, revision model.RevisionID, now time.Time) []ReReviewItem {
	state.mu.Lock()
	defer state.mu.Unlock()
	previous := state.current[eventID]
	state.current[eventID] = revision
	stamped := now.UTC()
	items := make([]ReReviewItem, 0)
	for _, track := range state.tracks[eventID] {
		stale := 0
		for _, cue := range track.Cues {
			if cue.Revision != revision {
				stale++
			}
		}
		if stale == 0 {
			continue
		}
		track.ReReviewPending = true
		track.ReReviewSince = &stamped
		items = append(items, ReReviewItem{
			EventID:   eventID,
			Language:  track.Language,
			From:      previous,
			Current:   revision,
			StaleCues: stale,
			Since:     stamped,
		})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Language < items[j].Language })
	return items
}

// SetCurrentRevision records the program revision an event is on without
// flagging tracks (used at registration and recovery).
func (state *State) SetCurrentRevision(eventID model.EventID, revision model.RevisionID) {
	state.mu.Lock()
	defer state.mu.Unlock()
	state.current[eventID] = revision
}

// CurrentRevision returns the revision last announced for an event.
func (state *State) CurrentRevision(eventID model.EventID) (model.RevisionID, bool) {
	state.mu.RLock()
	defer state.mu.RUnlock()
	revision, ok := state.current[eventID]
	return revision, ok
}

// ReReviewPending lists every track that still awaits re-review after a
// program revision.
func (state *State) ReReviewPending() []ReReviewItem {
	state.mu.RLock()
	defer state.mu.RUnlock()
	items := make([]ReReviewItem, 0)
	for eventID, languages := range state.tracks {
		current := state.current[eventID]
		for _, track := range languages {
			if !track.ReReviewPending {
				continue
			}
			item := ReReviewItem{
				EventID:  eventID,
				Language: track.Language,
				Current:  current,
				Since:    valueOrZero(track.ReReviewSince),
			}
			for _, cue := range track.Cues {
				if cue.Revision != current {
					item.StaleCues++
					if item.From == "" || cue.Revision < item.From {
						item.From = cue.Revision
					}
				}
			}
			items = append(items, item)
		}
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].EventID != items[j].EventID {
			return items[i].EventID < items[j].EventID
		}
		return items[i].Language < items[j].Language
	})
	return items
}

// LanguagesFor reports the registered language tags of one event.
func (state *State) LanguagesFor(eventID model.EventID) []Language {
	state.mu.RLock()
	defer state.mu.RUnlock()
	languages := make([]Language, 0, len(state.tracks[eventID]))
	for language := range state.tracks[eventID] {
		languages = append(languages, language)
	}
	sort.Slice(languages, func(i, j int) bool { return languages[i] < languages[j] })
	return languages
}

// Track returns a defensive copy of one language track.
func (state *State) Track(eventID model.EventID, language Language) (Track, bool) {
	state.mu.RLock()
	defer state.mu.RUnlock()
	track, ok := state.tracks[eventID][language]
	if !ok {
		return Track{}, false
	}
	return track.copy(), true
}

// TracksFor returns defensive copies of every language track of one event.
func (state *State) TracksFor(eventID model.EventID) []Track {
	state.mu.RLock()
	defer state.mu.RUnlock()
	tracks := make([]Track, 0, len(state.tracks[eventID]))
	for _, track := range state.tracks[eventID] {
		tracks = append(tracks, track.copy())
	}
	sort.Slice(tracks, func(i, j int) bool { return tracks[i].Language < tracks[j].Language })
	return tracks
}

func (state *State) lockedTrack(eventID model.EventID, language Language) (*Track, error) {
	languages := state.tracks[eventID]
	if languages == nil {
		return nil, fmt.Errorf("event %s has no subtitle tracks", eventID)
	}
	track, ok := languages[language]
	if !ok {
		return nil, fmt.Errorf("language %s is not registered for event %s", language, eventID)
	}
	return track, nil
}

func (track Track) copy() Track {
	clone := track
	clone.Cues = append([]Cue(nil), track.Cues...)
	return clone
}

func valueOrZero(value *time.Time) time.Time {
	if value == nil {
		return time.Time{}
	}
	return *value
}
