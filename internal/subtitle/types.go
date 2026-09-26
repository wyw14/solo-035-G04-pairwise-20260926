package subtitle

import (
	"fmt"
	"strings"
	"time"

	"github.com/wyw14/solo-035/internal/model"
)

// Language is a BCP-47 style language tag identifying a spoken-language
// subtitle track carried alongside the international signal.
type Language string

// Cue is one human-provided subtitle asset: translated text valid for a
// single timecode interval on one program revision of one language track.
type Cue struct {
	ID         string           `json:"id"`
	Language   Language         `json:"language"`
	Revision   model.RevisionID `json:"revision"`
	Text       string           `json:"text"`
	Start      int64            `json:"start_ms"`
	End        int64            `json:"end_ms"`
	Reviewed   bool             `json:"reviewed"`
	ReceivedAt time.Time        `json:"received_at"`
	ReviewedAt *time.Time       `json:"reviewed_at,omitempty"`
}

// Valid reports whether a cue is structurally sound. It deliberately does
// not check overlap or revision currency — those are audit concerns.
func (cue Cue) Valid() error {
	if cue.ID == "" {
		return fmt.Errorf("subtitle cue identity is empty")
	}
	if !cue.Language.Valid() {
		return fmt.Errorf("subtitle cue %s has no language", cue.ID)
	}
	if !cue.Revision.Valid() {
		return fmt.Errorf("subtitle cue %s carries an invalid program revision", cue.ID)
	}
	if strings.TrimSpace(cue.Text) == "" {
		return fmt.Errorf("subtitle cue %s text is empty", cue.ID)
	}
	if cue.Start < 0 || cue.End <= cue.Start {
		return fmt.Errorf("subtitle cue %s has an invalid timecode interval %d..%d", cue.ID, cue.Start, cue.End)
	}
	return nil
}

// Track is every cue ever received for one (event, language) pair, grouped
// by the program revision it was translated against.
type Track struct {
	EventID      model.EventID `json:"event_id"`
	Language     Language      `json:"language"`
	RegisteredAt time.Time     `json:"registered_at"`
	Cues         []Cue         `json:"cues"`
	// ReReviewPending marks a track whose cue set must be re-checked after a
	// program revision advanced beyond the revision its cues reference.
	ReReviewPending bool       `json:"re_review_pending"`
	ReReviewSince   *time.Time `json:"re_review_since,omitempty"`
}

func (language Language) Valid() bool {
	return strings.TrimSpace(string(language)) != ""
}

// IssueKind enumerates the audit findings an international event can have.
type IssueKind string

const (
	// IssueMissing means a reference interval has no cue on the track.
	IssueMissing IssueKind = "missing-translation"
	// IssueOverlap means two cues on the same track cover the same timecode.
	IssueOverlap IssueKind = "overlapping-cues"
	// IssueStale means a cue belongs to a program revision that is no longer current.
	IssueStale IssueKind = "stale-revision"
	// IssueUnreviewed means a cue has not passed human review.
	IssueUnreviewed IssueKind = "unreviewed-cue"
)

// Issue is one audit finding tied to a specific track.
type Issue struct {
	EventID  model.EventID    `json:"event_id"`
	Language Language         `json:"language"`
	Kind     IssueKind        `json:"kind"`
	Revision model.RevisionID `json:"revision,omitempty"`
	CueID    string           `json:"cue_id,omitempty"`
	StartMS  int64            `json:"start_ms,omitempty"`
	EndMS    int64            `json:"end_ms,omitempty"`
	Detail   string           `json:"detail"`
}

// AuditReport is the per-event result of checking every language track
// against the reference timeline and the current program revision.
type AuditReport struct {
	EventID         model.EventID    `json:"event_id"`
	CurrentRevision model.RevisionID `json:"current_revision"`
	Languages       []Language       `json:"languages"`
	Issues          []Issue          `json:"issues"`
	CheckedAt       time.Time        `json:"checked_at"`
}

// Healthy means every registered language is fully translated, reviewed,
// current and free of overlaps.
func (report AuditReport) Healthy() bool { return len(report.Issues) == 0 }

// Counts groups findings by kind, including zero values for every kind.
func (report AuditReport) Counts() map[IssueKind]int {
	counts := map[IssueKind]int{IssueMissing: 0, IssueOverlap: 0, IssueStale: 0, IssueUnreviewed: 0}
	for _, issue := range report.Issues {
		counts[issue.Kind]++
	}
	return counts
}

// ReReviewItem names a language track that must be re-audited because the
// program was revised after its cues were translated.
type ReReviewItem struct {
	EventID   model.EventID    `json:"event_id"`
	Language  Language         `json:"language"`
	From      model.RevisionID `json:"from_revision"`
	Current   model.RevisionID `json:"current_revision"`
	StaleCues int              `json:"stale_cues"`
	Since     time.Time        `json:"since"`
}

// PlaybackCue is the broadcast-facing projection of one cue.
type PlaybackCue struct {
	Language Language `json:"language"`
	Text     string   `json:"text"`
	StartMS  int64    `json:"start_ms"`
	EndMS    int64    `json:"end_ms"`
}
