package subtitle

import (
	"fmt"
	"time"

	"github.com/wyw14/solo-035/internal/model"
)

// RecoveryRecord is the persisted form of one event's subtitle assets.
type RecoveryRecord struct {
	EventID  model.EventID    `json:"event_id"`
	Revision model.RevisionID `json:"revision"`
	Tracks   []Track          `json:"tracks"`
}

// ExportRecovery snapshots every event for checkpoint persistence.
func (state *State) ExportRecovery() []RecoveryRecord {
	state.mu.RLock()
	defer state.mu.RUnlock()
	records := make([]RecoveryRecord, 0, len(state.tracks))
	for eventID, languages := range state.tracks {
		record := RecoveryRecord{EventID: eventID, Revision: state.current[eventID]}
		for _, track := range languages {
			record.Tracks = append(record.Tracks, track.copy())
		}
		records = append(records, record)
	}
	return records
}

// Restore rebuilds subtitle state from a checkpoint, replacing existing
// assets for the events contained in it.
func (state *State) Restore(records []RecoveryRecord, now time.Time) error {
	state.mu.Lock()
	defer state.mu.Unlock()
	for _, record := range records {
		if !record.EventID.Valid() {
			return fmt.Errorf("invalid recovered subtitle event identity")
		}
		languages := make(map[Language]*Track, len(record.Tracks))
		for index := range record.Tracks {
			track := record.Tracks[index]
			if !track.Language.Valid() {
				return fmt.Errorf("event %s has a recovered track without a language", record.EventID)
			}
			track.EventID = record.EventID
			for _, cue := range track.Cues {
				if err := cue.Valid(); err != nil {
					return fmt.Errorf("event %s track %s: %w", record.EventID, track.Language, err)
				}
			}
			languages[track.Language] = &record.Tracks[index]
		}
		state.tracks[record.EventID] = languages
		if record.Revision.Valid() {
			state.current[record.EventID] = record.Revision
		}
	}
	return nil
}
