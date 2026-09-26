package subtitle

import (
	"testing"
	"time"

	"github.com/wyw14/solo-035/internal/model"
)

func newTestCue(id, language string, revision model.RevisionID, text string, start, end int64) Cue {
	return Cue{ID: id, Language: Language(language), Revision: revision, Text: text, Start: start, End: end}
}

func seedTrack(t *testing.T, state *State, eventID model.EventID, revision model.RevisionID, language string, ranges [][2]int64, reviewed bool) {
	t.Helper()
	if _, err := state.RegisterLanguage(eventID, Language(language), time.Unix(1, 0).UTC()); err != nil {
		t.Fatal(err)
	}
	for index, span := range ranges {
		cue := newTestCue(language+"-"+string(rune('a'+index)), language, revision, "text", span[0], span[1])
		if err := state.Submit(eventID, cue); err != nil {
			t.Fatal(err)
		}
		if reviewed {
			if _, err := state.MarkReviewed(eventID, Language(language), cue.ID, time.Unix(2, 0).UTC()); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func TestAuditReportsMissingOverlapStaleAndUnreviewed(t *testing.T) {
	clock := func() time.Time { return time.Unix(10, 0).UTC() }
	state := NewState()
	auditor := NewAuditor(state, clock)
	eventID := model.NewEventID()
	oldRevision := model.NewRevisionID()
	newRevision := model.NewRevisionID()

	// Reference (original) language fully covered and reviewed on the new revision.
	seedTrack(t, state, eventID, newRevision, "zh-CN", [][2]int64{{0, 1000}, {1000, 2000}}, true)
	state.SetCurrentRevision(eventID, newRevision)

	// English: a gap and an overlap on the new revision, one reviewed cue,
	// and an overlapping pair on the old revision that becomes stale.
	if _, err := state.RegisterLanguage(eventID, "en", time.Unix(1, 0).UTC()); err != nil {
		t.Fatal(err)
	}
	reviewed := newTestCue("en-a", "en", newRevision, "welcome", 0, 1000)
	if err := state.Submit(eventID, reviewed); err != nil {
		t.Fatal(err)
	}
	if _, err := state.MarkReviewed(eventID, "en", reviewed.ID, time.Unix(2, 0).UTC()); err != nil {
		t.Fatal(err)
	}
	for _, cue := range []Cue{
		newTestCue("en-b", "en", newRevision, "overlap", 900, 1500), // overlaps en-a
		newTestCue("en-c", "en", newRevision, "tail", 1700, 2000),   // leaves 1500..1700 uncovered
		newTestCue("en-old-1", "en", oldRevision, "old one", 3000, 4000),
		newTestCue("en-old-2", "en", oldRevision, "old two", 3500, 4500),
	} {
		if err := state.Submit(eventID, cue); err != nil {
			t.Fatal(err)
		}
	}

	reference, err := auditor.ReferenceFromTrack(eventID, "zh-CN", newRevision)
	if err != nil {
		t.Fatal(err)
	}
	report := auditor.AuditEvent(eventID, newRevision, reference)

	counts := report.Counts()
	if counts[IssueMissing] != 1 {
		t.Fatalf("missing = %d, want 1", counts[IssueMissing])
	}
	if counts[IssueOverlap] != 1 {
		t.Fatalf("overlap = %d, want 1 (only same-revision cues overlap)", counts[IssueOverlap])
	}
	if counts[IssueStale] != 2 {
		t.Fatalf("stale = %d, want 2", counts[IssueStale])
	}
	if counts[IssueUnreviewed] != 2 {
		t.Fatalf("unreviewed = %d, want 2", counts[IssueUnreviewed])
	}
	if report.Healthy() {
		t.Fatal("report must not be healthy with findings")
	}
}

func TestTouchingIntervalsAreSequentialNotMissingOrOverlap(t *testing.T) {
	clock := func() time.Time { return time.Unix(10, 0).UTC() }
	state := NewState()
	auditor := NewAuditor(state, clock)
	eventID := model.NewEventID()
	revision := model.NewRevisionID()
	seedTrack(t, state, eventID, revision, "src", [][2]int64{{0, 1000}, {1000, 2000}}, true)
	seedTrack(t, state, eventID, revision, "dst", [][2]int64{{0, 1000}, {1000, 2000}}, true)
	reference, err := auditor.ReferenceFromTrack(eventID, "src", revision)
	if err != nil {
		t.Fatal(err)
	}
	report := auditor.AuditEvent(eventID, revision, reference)
	if !report.Healthy() {
		t.Fatalf("aligned tracks must be healthy, issues = %v", report.Issues)
	}
}

func TestProgramRevisionQueuesTracksForReReview(t *testing.T) {
	state := NewState()
	coordinator := NewCoordinator(state, func() time.Time { return time.Unix(10, 0).UTC() })
	eventID := model.NewEventID()
	revision := model.NewRevisionID()
	if _, err := coordinator.RegisterTrack(eventID, "en", revision); err != nil {
		t.Fatal(err)
	}
	cue, err := coordinator.SubmitCue(eventID, newTestCue("c1", "en", revision, "hi", 0, 1000))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := coordinator.ReviewCue(eventID, "en", cue.ID); err != nil {
		t.Fatal(err)
	}

	next := model.NewRevisionID()
	items := coordinator.ProgramRevised(eventID, next)
	if len(items) != 1 || items[0].Language != "en" || items[0].StaleCues != 1 {
		t.Fatalf("re-review queue = %+v", items)
	}

	// Translating and reviewing the full new revision clears the queue.
	cue2, err := coordinator.SubmitCue(eventID, newTestCue("c2", "en", next, "hi again", 0, 1000))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := coordinator.ReviewCue(eventID, "en", cue2.ID); err != nil {
		t.Fatal(err)
	}
	if pending := coordinator.ReReviewQueue(); len(pending) != 0 {
		t.Fatalf("queue should be cleared after current cues reviewed, got %+v", pending)
	}
}

func TestActiveAtServesOnlyReviewedCurrentCues(t *testing.T) {
	state := NewState()
	coordinator := NewCoordinator(state, func() time.Time { return time.Unix(10, 0).UTC() })
	eventID := model.NewEventID()
	revision := model.NewRevisionID()
	if _, err := coordinator.RegisterTrack(eventID, "en", revision); err != nil {
		t.Fatal(err)
	}
	reviewed, err := coordinator.SubmitCue(eventID, newTestCue("ok", "en", revision, "good evening", 0, 2000))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := coordinator.SubmitCue(eventID, newTestCue("draft", "en", revision, "not reviewed", 2000, 4000)); err != nil {
		t.Fatal(err)
	}
	if _, err := coordinator.ReviewCue(eventID, "en", reviewed.ID); err != nil {
		t.Fatal(err)
	}

	cue, err := coordinator.ActiveAt(eventID, "en", 1500)
	if err != nil {
		t.Fatal(err)
	}
	if cue.Text != "good evening" {
		t.Fatalf("active text = %q", cue.Text)
	}
	// End boundary is exclusive.
	if cue, err := coordinator.ActiveAt(eventID, "en", 2000); err != nil || cue.Text != "" {
		t.Fatalf("endpoint cue = %+v, err = %v", cue, err)
	}
	// Unreviewed cues are not broadcast.
	if cue, err := coordinator.ActiveAt(eventID, "en", 3000); err != nil || cue.Text != "" {
		t.Fatalf("unreviewed cue served: %+v", cue)
	}
	// Revision advancement retires every cue until retranslated.
	next := model.NewRevisionID()
	coordinator.ProgramRevised(eventID, next)
	if cue, err := coordinator.ActiveAt(eventID, "en", 1500); err != nil || cue.Text != "" {
		t.Fatalf("stale cue served after revision: %+v", cue)
	}

	window, err := coordinator.Window(eventID, "en", 0, 10000)
	if err != nil || len(window) != 0 {
		t.Fatalf("window after revision = %+v, err = %v", window, err)
	}
}

func TestRecoveryRoundTrip(t *testing.T) {
	state := NewState()
	eventID := model.NewEventID()
	revision := model.NewRevisionID()
	seedTrack(t, state, eventID, revision, "en", [][2]int64{{0, 1000}}, true)
	state.SetCurrentRevision(eventID, revision)
	records := state.ExportRecovery()
	if len(records) != 1 {
		t.Fatalf("records = %d", len(records))
	}

	restored := NewState()
	if err := restored.Restore(records, time.Unix(20, 0).UTC()); err != nil {
		t.Fatal(err)
	}
	track, ok := restored.Track(eventID, "en")
	if !ok || len(track.Cues) != 1 || !track.Cues[0].Reviewed {
		t.Fatalf("restored track = %+v ok = %v", track, ok)
	}
	if current, ok := restored.CurrentRevision(eventID); !ok || current != revision {
		t.Fatalf("restored revision = %s ok = %v", current, ok)
	}
}
