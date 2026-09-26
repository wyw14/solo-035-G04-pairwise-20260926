package caption

import (
	"strings"
	"testing"
	"time"

	"github.com/wyw14/solo-035/internal/model"
)

func testClock() time.Time {
	return time.Unix(1000, 0).UTC()
}

func ingestCue(t *testing.T, library *Library, eventID model.EventID, revision model.RevisionID, language string, sequence int64, start int64, end int64) SubtitleCue {
	t.Helper()
	cue, err := library.Ingest(SubtitleCue{
		EventID:  eventID,
		Language: language,
		Revision: revision,
		Version:  1,
		Sequence: sequence,
		Start:    start,
		End:      end,
		Text:     "cue",
	})
	if err != nil {
		t.Fatalf("ingest %s/%d: %v", language, sequence, err)
	}
	return cue
}

func TestIngestValidatesCue(t *testing.T) {
	library := NewLibrary(testClock)
	eventID := model.NewEventID()
	revision := model.NewRevisionID()
	base := SubtitleCue{EventID: eventID, Language: "en", Revision: revision, Version: 1, Sequence: 1, Start: 10, End: 20, Text: "hello"}

	cases := []struct {
		name   string
		mutate func(*SubtitleCue)
		want   string
	}{
		{"empty language", func(cue *SubtitleCue) { cue.Language = " " }, "language is required"},
		{"empty text", func(cue *SubtitleCue) { cue.Text = "" }, "text is empty"},
		{"end before start", func(cue *SubtitleCue) { cue.End = 5 }, "is not after start"},
		{"zero version", func(cue *SubtitleCue) { cue.Version = 0 }, "version must be positive"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			cue := base
			testCase.mutate(&cue)
			if _, err := library.Ingest(cue); err == nil || !strings.Contains(err.Error(), testCase.want) {
				t.Fatalf("error = %v, want %q", err, testCase.want)
			}
		})
	}
	if _, err := library.Ingest(base); err != nil {
		t.Fatalf("valid cue rejected: %v", err)
	}
}

func TestIngestRejectsVersionRegression(t *testing.T) {
	library := NewLibrary(testClock)
	eventID := model.NewEventID()
	revision := model.NewRevisionID()
	cue := SubtitleCue{EventID: eventID, Language: "EN", Revision: revision, Version: 2, Sequence: 1, Start: 10, End: 20, Text: "v2"}
	if _, err := library.Ingest(cue); err != nil {
		t.Fatal(err)
	}
	cue.Version = 1
	cue.Text = "v1"
	if _, err := library.Ingest(cue); err == nil || !strings.Contains(err.Error(), "regresses") {
		t.Fatalf("error = %v, want version regression", err)
	}
	cue.Version = 3
	cue.Text = "v3"
	stored, err := library.Ingest(cue)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Language != "en" || stored.Text != "v3" {
		t.Fatalf("stored = %+v", stored)
	}
}

func TestReportFindsMissingOverlapAndExpired(t *testing.T) {
	library := NewLibrary(testClock)
	eventID := model.NewEventID()
	revision := model.NewRevisionID()
	ingestCue(t, library, eventID, revision, "en", 1, 100, 200)
	ingestCue(t, library, eventID, revision, "en", 2, 200, 300)
	ingestCue(t, library, eventID, revision, "fr", 1, 100, 200)
	// fr has no cue covering en cue 2 -> missing translation.
	// de overlaps itself and ends before the check timecode -> expired.
	ingestCue(t, library, eventID, revision, "de", 1, 100, 250)
	ingestCue(t, library, eventID, revision, "de", 2, 200, 260)
	if err := library.SetReference(eventID, "en"); err != nil {
		t.Fatal(err)
	}

	report := library.Report(eventID, revision, 500)
	if !report.ReferenceSet {
		t.Fatal("reference track was not resolved")
	}
	issues := make(map[string][]SubtitleIssue)
	for _, track := range report.Tracks {
		issues[track.Language] = track.Issues
	}
	var frMissing, frExpired int
	for _, issue := range issues["fr"] {
		switch issue.Kind {
		case IssueMissingTranslation:
			frMissing++
			if issue.Start != 200 || issue.End != 300 {
				t.Fatalf("fr missing window = %+v", issue)
			}
		case IssueExpired:
			frExpired++
		}
	}
	if frMissing != 1 || frExpired != 1 {
		t.Fatalf("fr missing = %d expired = %d", frMissing, frExpired)
	}
	var deOverlap, deExpired int
	for _, issue := range issues["de"] {
		switch issue.Kind {
		case IssueOverlap:
			deOverlap++
		case IssueExpired:
			deExpired++
		case IssueMissingTranslation:
			t.Fatalf("de covers the reference window, got %+v", issue)
		}
	}
	if deOverlap != 1 || deExpired != 2 {
		t.Fatalf("de overlap = %d expired = %d", deOverlap, deExpired)
	}
	if len(issues["en"]) != 2 { // both en cues expired at timecode 500
		t.Fatalf("en issues = %+v", issues["en"])
	}
	if report.Clean() {
		t.Fatal("report should not be clean")
	}
	for _, language := range []string{"de", "en", "fr"} {
		found := false
		for _, pending := range report.ReviewPending {
			found = found || pending == language
		}
		if !found {
			t.Fatalf("%s missing from review pending %v", language, report.ReviewPending)
		}
	}
}

func TestReviewClearsPendingForCurrentRevisionOnly(t *testing.T) {
	library := NewLibrary(testClock)
	eventID := model.NewEventID()
	revision := model.NewRevisionID()
	ingestCue(t, library, eventID, revision, "en", 1, 100, 200)
	if _, err := library.MarkReviewed(eventID, "en", revision); err != nil {
		t.Fatal(err)
	}
	report := library.Report(eventID, revision, 50)
	if len(report.ReviewPending) != 0 || !report.Clean() {
		t.Fatalf("report = %+v", report)
	}
	next := model.NewRevisionID()
	report = library.Report(eventID, next, 50)
	if len(report.ReviewPending) != 1 || report.ReviewPending[0] != "en" {
		t.Fatalf("review pending = %v", report.ReviewPending)
	}
	if _, err := library.MarkReviewed(eventID, "fr", revision); err == nil {
		t.Fatal("reviewing an unknown track should fail")
	}
}

func TestActiveReturnsOnAirCuesOfCurrentRevision(t *testing.T) {
	library := NewLibrary(testClock)
	eventID := model.NewEventID()
	revision := model.NewRevisionID()
	ingestCue(t, library, eventID, revision, "en", 1, 100, 200)
	ingestCue(t, library, eventID, revision, "en", 2, 200, 300)
	stale := SubtitleCue{EventID: eventID, Language: "en", Revision: model.NewRevisionID(), Version: 1, Sequence: 9, Start: 100, End: 900, Text: "stale"}
	if _, err := library.Ingest(stale); err != nil {
		t.Fatal(err)
	}

	active, err := library.Active(eventID, "en", revision, 150)
	if err != nil {
		t.Fatal(err)
	}
	if len(active) != 1 || active[0].Sequence != 1 {
		t.Fatalf("active = %+v", active)
	}
	active, err = library.Active(eventID, "en", revision, 200)
	if err != nil {
		t.Fatal(err)
	}
	if len(active) != 1 || active[0].Sequence != 2 {
		t.Fatalf("active at boundary = %+v", active)
	}
	if _, err := library.Active(eventID, "ja", revision, 150); err == nil {
		t.Fatal("unknown language track should fail")
	}
}

func TestExportRestoreRoundTrip(t *testing.T) {
	library := NewLibrary(testClock)
	eventID := model.NewEventID()
	revision := model.NewRevisionID()
	ingestCue(t, library, eventID, revision, "en", 1, 100, 200)
	ingestCue(t, library, eventID, revision, "fr", 1, 100, 200)
	if err := library.SetReference(eventID, "en"); err != nil {
		t.Fatal(err)
	}
	if _, err := library.MarkReviewed(eventID, "en", revision); err != nil {
		t.Fatal(err)
	}

	restored := NewLibrary(testClock)
	if err := restored.Restore(library.Export()); err != nil {
		t.Fatal(err)
	}
	report := restored.Report(eventID, revision, 150)
	if report.Reference != "en" || !report.ReferenceSet {
		t.Fatalf("reference = %q set = %v", report.Reference, report.ReferenceSet)
	}
	if len(report.ReviewPending) != 1 || report.ReviewPending[0] != "fr" {
		t.Fatalf("review pending = %v", report.ReviewPending)
	}
	active, err := restored.Active(eventID, "fr", revision, 150)
	if err != nil || len(active) != 1 {
		t.Fatalf("active = %+v err = %v", active, err)
	}

	broken := restored.Export()
	broken.Cues[0].End = broken.Cues[0].Start
	if err := NewLibrary(testClock).Restore(broken); err == nil {
		t.Fatal("invalid checkpoint should be rejected")
	}
}
