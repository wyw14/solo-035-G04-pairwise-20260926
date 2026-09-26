package caption

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/wyw14/solo-035/internal/model"
)

// SubtitleCue is one human-supplied translation on a language track. Start and
// End are source timecodes in the same units as model.Frame.Timecode; the cue
// is on air while Start <= timecode < End. Revision pins the cue to the
// program revision it was translated against; Version is the translator's own
// version of that cue slot and only ever moves forward.
type SubtitleCue struct {
	EventID     model.EventID    `json:"event_id"`
	Language    string           `json:"language"`
	Revision    model.RevisionID `json:"revision"`
	Version     uint64           `json:"version"`
	Sequence    int64            `json:"sequence"`
	Start       int64            `json:"start"`
	End         int64            `json:"end"`
	Text        string           `json:"text"`
	SubmittedAt time.Time        `json:"submitted_at"`
}

// TrackReview records that a language track was signed off for one program
// revision. A program revision change invalidates every earlier review.
type TrackReview struct {
	EventID    model.EventID    `json:"event_id"`
	Language   string           `json:"language"`
	Revision   model.RevisionID `json:"revision"`
	ReviewedAt time.Time        `json:"reviewed_at"`
}

// Recovery captures the whole subtitle library for checkpoints.
type Recovery struct {
	Cues       []SubtitleCue            `json:"cues"`
	Reviews    []TrackReview            `json:"reviews"`
	References map[model.EventID]string `json:"references,omitempty"`
}

const (
	IssueMissingTranslation = "missing-translation"
	IssueOverlap            = "overlap"
	IssueExpired            = "expired"
)

// SubtitleIssue is one alignment defect found on a language track.
type SubtitleIssue struct {
	Kind     string `json:"kind"`
	Language string `json:"language"`
	Start    int64  `json:"start"`
	End      int64  `json:"end"`
	Detail   string `json:"detail"`
}

// TrackView summarizes one language track against the current program revision.
type TrackView struct {
	Language            string `json:"language"`
	Cues                int    `json:"cues"`
	CurrentRevisionCues int    `json:"current_revision_cues"`
	LatestVersion       uint64 `json:"latest_version"`
	Reviewed            bool   `json:"reviewed"`
	NeedsReview         bool   `json:"needs_review"`
	Reference           bool   `json:"reference"`
}

// TrackReport lists the alignment defects found on one language track.
type TrackReport struct {
	Language    string          `json:"language"`
	Cues        int             `json:"cues"`
	NeedsReview bool            `json:"needs_review"`
	Issues      []SubtitleIssue `json:"issues,omitempty"`
}

// Report is the per-event alignment audit across every language track. Only
// cues pinned to the current program revision take part in the checks; cues
// left over from an older revision surface through ReviewPending instead.
type Report struct {
	EventID       model.EventID    `json:"event_id"`
	Revision      model.RevisionID `json:"revision"`
	Timecode      int64            `json:"timecode"`
	Reference     string           `json:"reference_language,omitempty"`
	ReferenceSet  bool             `json:"reference_set"`
	Tracks        []TrackReport    `json:"tracks"`
	ReviewPending []string         `json:"review_pending"`
	CheckedAt     time.Time        `json:"checked_at"`
}

// Clean reports whether the event has no alignment defects and no track
// waiting for a re-review.
func (report Report) Clean() bool {
	if len(report.ReviewPending) > 0 {
		return false
	}
	for _, track := range report.Tracks {
		if len(track.Issues) > 0 {
			return false
		}
	}
	return true
}

// IssueCount totals the defects across every track.
func (report Report) IssueCount() int {
	count := 0
	for _, track := range report.Tracks {
		count += len(track.Issues)
	}
	return count
}

// Library stores subtitle assets as one versioned language track set per
// live event.
type Library struct {
	mu         sync.RWMutex
	cues       map[model.EventID]map[string]map[int64]SubtitleCue
	reviews    map[model.EventID]map[string]TrackReview
	references map[model.EventID]string
	clock      func() time.Time
}

func NewLibrary(clock func() time.Time) *Library {
	if clock == nil {
		clock = time.Now
	}
	return &Library{
		cues:       make(map[model.EventID]map[string]map[int64]SubtitleCue),
		reviews:    make(map[model.EventID]map[string]TrackReview),
		references: make(map[model.EventID]string),
		clock:      clock,
	}
}

// NormalizeLanguage canonicalizes a BCP-47-ish language tag so "EN" and "en"
// land on the same track.
func NormalizeLanguage(language string) string {
	return strings.ToLower(strings.TrimSpace(language))
}

// Ingest accepts one human-translated cue. The cue replaces any earlier cue
// in the same (event, language, sequence) slot as long as its version does
// not regress.
func (library *Library) Ingest(cue SubtitleCue) (SubtitleCue, error) {
	cue.Language = NormalizeLanguage(cue.Language)
	if cue.Language == "" {
		return SubtitleCue{}, fmt.Errorf("subtitle language is required")
	}
	if strings.TrimSpace(cue.Text) == "" {
		return SubtitleCue{}, fmt.Errorf("subtitle cue text is empty")
	}
	if cue.End <= cue.Start {
		return SubtitleCue{}, fmt.Errorf("subtitle cue end %d is not after start %d", cue.End, cue.Start)
	}
	if cue.Version == 0 {
		return SubtitleCue{}, fmt.Errorf("subtitle cue version must be positive")
	}
	library.mu.Lock()
	defer library.mu.Unlock()
	track := library.trackLocked(cue.EventID, cue.Language)
	if existing, ok := track[cue.Sequence]; ok && cue.Version < existing.Version {
		return SubtitleCue{}, fmt.Errorf("subtitle cue %d version %d regresses below %d", cue.Sequence, cue.Version, existing.Version)
	}
	cue.SubmittedAt = library.clock().UTC()
	track[cue.Sequence] = cue
	return cue, nil
}

// SetReference picks the source-language track the other tracks are aligned
// against when checking for missing translations.
func (library *Library) SetReference(eventID model.EventID, language string) error {
	language = NormalizeLanguage(language)
	if language == "" {
		return fmt.Errorf("reference language is required")
	}
	library.mu.Lock()
	defer library.mu.Unlock()
	library.references[eventID] = language
	return nil
}

// Reference returns the reference language for an event, if one was chosen.
func (library *Library) Reference(eventID model.EventID) string {
	library.mu.RLock()
	defer library.mu.RUnlock()
	return library.references[eventID]
}

// MarkReviewed signs a language track off for one program revision.
func (library *Library) MarkReviewed(eventID model.EventID, language string, revision model.RevisionID) (TrackReview, error) {
	language = NormalizeLanguage(language)
	if language == "" {
		return TrackReview{}, fmt.Errorf("subtitle language is required")
	}
	library.mu.Lock()
	defer library.mu.Unlock()
	if _, ok := library.cues[eventID][language]; !ok {
		return TrackReview{}, fmt.Errorf("language track %q is not registered for event %s", language, eventID)
	}
	review := TrackReview{EventID: eventID, Language: language, Revision: revision, ReviewedAt: library.clock().UTC()}
	if library.reviews[eventID] == nil {
		library.reviews[eventID] = make(map[string]TrackReview)
	}
	library.reviews[eventID][language] = review
	return review, nil
}

// Reviewed reports whether a track was signed off for the given revision.
func (library *Library) Reviewed(eventID model.EventID, language string, revision model.RevisionID) bool {
	library.mu.RLock()
	defer library.mu.RUnlock()
	review, ok := library.reviews[eventID][NormalizeLanguage(language)]
	return ok && review.Revision == revision
}

// Tracks summarizes every language track of an event against one revision.
func (library *Library) Tracks(eventID model.EventID, revision model.RevisionID) []TrackView {
	library.mu.RLock()
	defer library.mu.RUnlock()
	reference := library.references[eventID]
	languages := library.languagesLocked(eventID)
	views := make([]TrackView, 0, len(languages))
	for _, language := range languages {
		view := TrackView{Language: language, Reference: language == reference}
		for _, cue := range library.cues[eventID][language] {
			view.Cues++
			if cue.Version > view.LatestVersion {
				view.LatestVersion = cue.Version
			}
			if cue.Revision == revision {
				view.CurrentRevisionCues++
			}
		}
		review, ok := library.reviews[eventID][language]
		view.Reviewed = ok && review.Revision == revision
		view.NeedsReview = !view.Reviewed || view.CurrentRevisionCues == 0
		views = append(views, view)
	}
	return views
}

// Report audits every language track of an event at one timecode: missing
// translations against the reference track, overlapping cues inside a track,
// and cues whose display window has already closed.
func (library *Library) Report(eventID model.EventID, revision model.RevisionID, timecode int64) Report {
	library.mu.RLock()
	defer library.mu.RUnlock()
	report := Report{
		EventID:   eventID,
		Revision:  revision,
		Timecode:  timecode,
		Reference: library.references[eventID],
		CheckedAt: library.clock().UTC(),
	}
	current := make(map[string][]SubtitleCue)
	for _, language := range library.languagesLocked(eventID) {
		current[language] = cuesAtRevision(library.cues[eventID][language], revision)
	}
	referenceCues := current[report.Reference]
	report.ReferenceSet = report.Reference != "" && len(referenceCues) > 0
	for _, language := range library.languagesLocked(eventID) {
		cues := current[language]
		track := TrackReport{Language: language, Cues: len(cues)}
		review, ok := library.reviews[eventID][language]
		track.NeedsReview = !(ok && review.Revision == revision) || len(cues) == 0
		if track.NeedsReview {
			report.ReviewPending = append(report.ReviewPending, language)
		}
		track.Issues = append(track.Issues, overlapIssues(language, cues)...)
		for _, cue := range cues {
			if cue.End <= timecode {
				track.Issues = append(track.Issues, SubtitleIssue{
					Kind:     IssueExpired,
					Language: language,
					Start:    cue.Start,
					End:      cue.End,
					Detail:   fmt.Sprintf("cue %d expired at timecode %d (now %d)", cue.Sequence, cue.End, timecode),
				})
			}
		}
		if report.ReferenceSet && language != report.Reference {
			for _, reference := range referenceCues {
				if !coveredBy(cues, reference) {
					track.Issues = append(track.Issues, SubtitleIssue{
						Kind:     IssueMissingTranslation,
						Language: language,
						Start:    reference.Start,
						End:      reference.End,
						Detail:   fmt.Sprintf("no %s cue covers reference cue %d", language, reference.Sequence),
					})
				}
			}
		}
		report.Tracks = append(report.Tracks, track)
	}
	return report
}

// Active returns the cues of one language track that are on air at a
// timecode: pinned to the current revision and within their display window.
func (library *Library) Active(eventID model.EventID, language string, revision model.RevisionID, timecode int64) ([]SubtitleCue, error) {
	language = NormalizeLanguage(language)
	library.mu.RLock()
	defer library.mu.RUnlock()
	track, ok := library.cues[eventID][language]
	if !ok {
		return nil, fmt.Errorf("language track %q is not registered for event %s", language, eventID)
	}
	active := make([]SubtitleCue, 0, len(track))
	for _, cue := range track {
		if cue.Revision == revision && cue.Start <= timecode && timecode < cue.End {
			active = append(active, cue)
		}
	}
	sort.Slice(active, func(i, j int) bool {
		if active[i].Start == active[j].Start {
			return active[i].Sequence < active[j].Sequence
		}
		return active[i].Start < active[j].Start
	})
	return active, nil
}

// Export snapshots the library for a checkpoint.
func (library *Library) Export() Recovery {
	library.mu.RLock()
	defer library.mu.RUnlock()
	recovery := Recovery{References: make(map[model.EventID]string, len(library.references))}
	for eventID, track := range library.cues {
		for _, cues := range track {
			for _, cue := range cues {
				recovery.Cues = append(recovery.Cues, cue)
			}
		}
		for _, review := range library.reviews[eventID] {
			recovery.Reviews = append(recovery.Reviews, review)
		}
	}
	for eventID, reviews := range library.reviews {
		if _, ok := library.cues[eventID]; !ok {
			for _, review := range reviews {
				recovery.Reviews = append(recovery.Reviews, review)
			}
		}
	}
	for eventID, reference := range library.references {
		recovery.References[eventID] = reference
	}
	sort.Slice(recovery.Cues, func(i, j int) bool {
		if recovery.Cues[i].EventID != recovery.Cues[j].EventID {
			return recovery.Cues[i].EventID < recovery.Cues[j].EventID
		}
		if recovery.Cues[i].Language != recovery.Cues[j].Language {
			return recovery.Cues[i].Language < recovery.Cues[j].Language
		}
		return recovery.Cues[i].Sequence < recovery.Cues[j].Sequence
	})
	sort.Slice(recovery.Reviews, func(i, j int) bool {
		if recovery.Reviews[i].EventID != recovery.Reviews[j].EventID {
			return recovery.Reviews[i].EventID < recovery.Reviews[j].EventID
		}
		return recovery.Reviews[i].Language < recovery.Reviews[j].Language
	})
	return recovery
}

// Restore replaces the library contents with a checkpoint snapshot.
func (library *Library) Restore(recovery Recovery) error {
	cues := make(map[model.EventID]map[string]map[int64]SubtitleCue)
	for _, cue := range recovery.Cues {
		cue.Language = NormalizeLanguage(cue.Language)
		if !cue.EventID.Valid() || !cue.Revision.Valid() {
			return fmt.Errorf("recovered subtitle cue has an invalid identity")
		}
		if cue.Language == "" || cue.Version == 0 || cue.End <= cue.Start {
			return fmt.Errorf("recovered subtitle cue %d for event %s is incomplete", cue.Sequence, cue.EventID)
		}
		if cues[cue.EventID] == nil {
			cues[cue.EventID] = make(map[string]map[int64]SubtitleCue)
		}
		if cues[cue.EventID][cue.Language] == nil {
			cues[cue.EventID][cue.Language] = make(map[int64]SubtitleCue)
		}
		track := cues[cue.EventID][cue.Language]
		if existing, ok := track[cue.Sequence]; !ok || existing.Version <= cue.Version {
			track[cue.Sequence] = cue
		}
	}
	reviews := make(map[model.EventID]map[string]TrackReview)
	for _, review := range recovery.Reviews {
		review.Language = NormalizeLanguage(review.Language)
		if !review.EventID.Valid() || !review.Revision.Valid() || review.Language == "" {
			return fmt.Errorf("recovered track review is incomplete")
		}
		if reviews[review.EventID] == nil {
			reviews[review.EventID] = make(map[string]TrackReview)
		}
		reviews[review.EventID][review.Language] = review
	}
	references := make(map[model.EventID]string, len(recovery.References))
	for eventID, reference := range recovery.References {
		reference = NormalizeLanguage(reference)
		if !eventID.Valid() || reference == "" {
			return fmt.Errorf("recovered reference language is incomplete")
		}
		references[eventID] = reference
	}
	library.mu.Lock()
	defer library.mu.Unlock()
	library.cues = cues
	library.reviews = reviews
	library.references = references
	return nil
}

func (library *Library) trackLocked(eventID model.EventID, language string) map[int64]SubtitleCue {
	if library.cues[eventID] == nil {
		library.cues[eventID] = make(map[string]map[int64]SubtitleCue)
	}
	if library.cues[eventID][language] == nil {
		library.cues[eventID][language] = make(map[int64]SubtitleCue)
	}
	return library.cues[eventID][language]
}

func (library *Library) languagesLocked(eventID model.EventID) []string {
	languages := make([]string, 0, len(library.cues[eventID]))
	for language := range library.cues[eventID] {
		languages = append(languages, language)
	}
	sort.Strings(languages)
	return languages
}

func cuesAtRevision(track map[int64]SubtitleCue, revision model.RevisionID) []SubtitleCue {
	cues := make([]SubtitleCue, 0, len(track))
	for _, cue := range track {
		if cue.Revision == revision {
			cues = append(cues, cue)
		}
	}
	sort.Slice(cues, func(i, j int) bool {
		if cues[i].Start == cues[j].Start {
			return cues[i].Sequence < cues[j].Sequence
		}
		return cues[i].Start < cues[j].Start
	})
	return cues
}

func overlapIssues(language string, cues []SubtitleCue) []SubtitleIssue {
	var issues []SubtitleIssue
	widest := -1
	for index, cue := range cues {
		if widest >= 0 && cue.Start < cues[widest].End {
			issues = append(issues, SubtitleIssue{
				Kind:     IssueOverlap,
				Language: language,
				Start:    cue.Start,
				End:      cue.End,
				Detail:   fmt.Sprintf("cue %d overlaps cue %d", cue.Sequence, cues[widest].Sequence),
			})
		}
		if widest < 0 || cue.End > cues[widest].End {
			widest = index
		}
	}
	return issues
}

// coveredBy treats any overlapping translation cue as covering the reference
// cue: translators routinely shift timing by a few frames.
func coveredBy(cues []SubtitleCue, reference SubtitleCue) bool {
	for _, cue := range cues {
		if cue.Start < reference.End && cue.End > reference.Start {
			return true
		}
	}
	return false
}
