package service

import (
	"context"
	"testing"
	"time"
)

func bootstrapRuntime(t *testing.T) *Runtime {
	t.Helper()
	runtime, err := NewRuntime(t.TempDir(), func() time.Time { return time.Unix(100, 0).UTC() })
	if err != nil {
		t.Fatal(err)
	}
	if err := Bootstrap(runtime); err != nil {
		t.Fatal(err)
	}
	return runtime
}

func onlyEventID(t *testing.T, runtime *Runtime) string {
	t.Helper()
	events := runtime.Events.List()
	if len(events) != 1 {
		t.Fatalf("events = %d", len(events))
	}
	return string(events[0].ID)
}

func TestCaptionTrackLifecycleAcrossProgramRevision(t *testing.T) {
	runtime := bootstrapRuntime(t)
	eventID := onlyEventID(t, runtime)

	ingest := func(language string, sequence int64, start int64, end int64) {
		t.Helper()
		_, err := runtime.IngestCaptionCue(CaptionCueInput{
			EventID: eventID, Language: language, Version: 1,
			Sequence: sequence, Start: start, End: end, Text: "translated",
		})
		if err != nil {
			t.Fatalf("ingest %s/%d: %v", language, sequence, err)
		}
	}
	ingest("en", 1, 1000, 1100)
	ingest("en", 2, 1100, 1200)
	ingest("fr", 1, 1000, 1100)

	report, err := runtime.CaptionReport(eventID, "en", nil)
	if err != nil {
		t.Fatal(err)
	}
	if report.Timecode != 1003 { // last timecode pushed by Bootstrap
		t.Fatalf("report timecode = %d", report.Timecode)
	}
	var frIssues int
	for _, track := range report.Tracks {
		if track.Language == "fr" {
			frIssues = len(track.Issues)
		}
	}
	if frIssues != 1 {
		t.Fatalf("fr should miss one translation, issues = %d", frIssues)
	}
	if len(report.ReviewPending) != 2 {
		t.Fatalf("review pending = %v", report.ReviewPending)
	}

	if _, err := runtime.ReviewCaptionTrack(eventID, "en"); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.ReviewCaptionTrack(eventID, "fr"); err != nil {
		t.Fatal(err)
	}
	tracks, err := runtime.CaptionTracks(eventID)
	if err != nil {
		t.Fatal(err)
	}
	for _, track := range tracks {
		if track.NeedsReview {
			t.Fatalf("track %s still needs review: %+v", track.Language, track)
		}
	}

	// A program revision forces every track back onto the re-review list.
	if _, err := runtime.ExecuteControl(context.Background(), ControlRequest{Action: "advance-program", EventID: eventID}); err != nil {
		t.Fatal(err)
	}
	tracks, err = runtime.CaptionTracks(eventID)
	if err != nil {
		t.Fatal(err)
	}
	if len(tracks) != 2 {
		t.Fatalf("tracks = %+v", tracks)
	}
	for _, track := range tracks {
		if !track.NeedsReview || track.Reviewed {
			t.Fatalf("track %s should need re-review after revision: %+v", track.Language, track)
		}
	}

	// Old-revision cues must not leak into the broadcast query.
	timecode := int64(1050)
	active, err := runtime.ActiveCaptions(eventID, "en", &timecode)
	if err != nil {
		t.Fatal(err)
	}
	if len(active.Cues) != 0 {
		t.Fatalf("stale cues on air: %+v", active.Cues)
	}
}

func TestActiveCaptionsUsesEventTimecode(t *testing.T) {
	runtime := bootstrapRuntime(t)
	eventID := onlyEventID(t, runtime)
	if _, err := runtime.IngestCaptionCue(CaptionCueInput{
		EventID: eventID, Language: "en", Version: 1, Sequence: 1, Start: 1000, End: 1100, Text: "on air",
	}); err != nil {
		t.Fatal(err)
	}
	active, err := runtime.ActiveCaptions(eventID, "en", nil)
	if err != nil {
		t.Fatal(err)
	}
	if active.Timecode != 1003 || len(active.Cues) != 1 || active.Cues[0].Text != "on air" {
		t.Fatalf("active = %+v", active)
	}
	if active.Reviewed {
		t.Fatal("track was never reviewed")
	}
	if _, err := runtime.ActiveCaptions(eventID, "ja", nil); err == nil {
		t.Fatal("unknown language should fail")
	}
	if _, err := runtime.ActiveCaptions("no-such-event", "en", nil); err == nil {
		t.Fatal("unknown event should fail")
	}
}

func TestCaptionLibrarySurvivesCheckpoint(t *testing.T) {
	runtime := bootstrapRuntime(t)
	eventID := onlyEventID(t, runtime)
	if _, err := runtime.IngestCaptionCue(CaptionCueInput{
		EventID: eventID, Language: "en", Version: 1, Sequence: 1, Start: 1000, End: 1100, Text: "checkpoint",
	}); err != nil {
		t.Fatal(err)
	}
	checkpoint := runtime.ExportCheckpoint()
	if len(checkpoint.Captions.Cues) != 1 {
		t.Fatalf("checkpoint cues = %d", len(checkpoint.Captions.Cues))
	}

	fresh, err := NewRuntime(t.TempDir(), func() time.Time { return time.Unix(100, 0).UTC() })
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fresh.RestoreCheckpoint(checkpoint); err != nil {
		t.Fatal(err)
	}
	timecode := int64(1050)
	active, err := fresh.ActiveCaptions(eventID, "en", &timecode)
	if err != nil {
		t.Fatal(err)
	}
	if len(active.Cues) != 1 || active.Cues[0].Text != "checkpoint" {
		t.Fatalf("active = %+v", active)
	}
}

func TestCaptionTrackInterlockReflectsAudit(t *testing.T) {
	runtime := bootstrapRuntime(t)
	eventID := onlyEventID(t, runtime)
	passed := func() (bool, bool) {
		for _, interlock := range runtime.Snapshot().Interlocks {
			if interlock.Name == "caption-tracks" {
				return interlock.Passed, true
			}
		}
		return false, false
	}
	if ok, found := passed(); !found || !ok {
		t.Fatalf("interlock should pass with no tracks: %v %v", ok, found)
	}
	if _, err := runtime.IngestCaptionCue(CaptionCueInput{
		EventID: eventID, Language: "en", Version: 1, Sequence: 1, Start: 1000, End: 1002, Text: "expired",
	}); err != nil {
		t.Fatal(err)
	}
	ok, found := passed()
	if !found || ok {
		t.Fatalf("interlock should fail with an expired unreviewed track: %v %v", ok, found)
	}
}
