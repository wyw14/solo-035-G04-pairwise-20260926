package service

import (
	"fmt"
	"sort"

	"github.com/google/uuid"
	"github.com/wyw14/solo-035/internal/model"
	"github.com/wyw14/solo-035/internal/subtitle"
)

func (runtime *Runtime) Snapshot() model.RuntimeSnapshot {
	snapshot := model.RuntimeSnapshot{CapturedAt: runtime.Clock().UTC()}
	for _, event := range runtime.Events.List() {
		snapshot.Operations = append(snapshot.Operations, model.OperationView{
			EventID:          event.ID,
			Name:             event.Name,
			Phase:            event.Phase,
			ProgramRevision:  event.Revision,
			SourceGeneration: event.SourceGeneration,
			CommandEpoch:     event.CommandEpoch,
		})
		status, synced := runtime.Sync.Status(event.ID)
		snapshot.Interlocks = append(snapshot.Interlocks, model.InterlockView{EventID: event.ID, Name: "frame-sync", Passed: synced && status.Stable, Detail: status.Reason})
		captionStatus, captionKnown := runtime.Captions.Get(event.ID)
		captionPassed := captionKnown && captionStatus.Continuous && captionStatus.EncoderErr == ""
		snapshot.Interlocks = append(snapshot.Interlocks, model.InterlockView{EventID: event.ID, Name: "caption-continuity", Passed: captionPassed, Detail: captionStatus.EncoderErr})
		if captionStatus.EncoderErr != "" {
			snapshot.Incidents = append(snapshot.Incidents, model.IncidentView{ID: uuid.NewString(), EventID: event.ID, Severity: "error", Message: captionStatus.EncoderErr, CreatedAt: captionStatus.UpdatedAt})
		}
		if report, err := runtime.SubtitleAudit(event.ID, ""); err == nil && len(report.Languages) > 0 {
			counts := report.Counts()
			detail := fmt.Sprintf("%d languages aligned", len(report.Languages))
			if !report.Healthy() {
				detail = fmt.Sprintf("missing=%d overlap=%d stale=%d unreviewed=%d",
					counts[subtitle.IssueMissing], counts[subtitle.IssueOverlap], counts[subtitle.IssueStale], counts[subtitle.IssueUnreviewed])
			}
			snapshot.Interlocks = append(snapshot.Interlocks, model.InterlockView{EventID: event.ID, Name: "subtitle-tracks", Passed: report.Healthy(), Detail: detail})
			for _, issue := range report.Issues {
				if issue.Kind != subtitle.IssueStale && issue.Kind != subtitle.IssueMissing {
					continue
				}
				snapshot.Incidents = append(snapshot.Incidents, model.IncidentView{ID: uuid.NewString(), EventID: event.ID, Severity: "warning", Message: issue.Detail, CreatedAt: report.CheckedAt})
			}
		}
	}
	for _, resource := range runtime.EncoderFlow.Equipment() {
		snapshot.Equipment = append(snapshot.Equipment, model.EquipmentView{ID: resource.ID, Kind: "encoder", Owner: resource.Lease.Owner, Fence: resource.Lease.Fence, Healthy: resource.Healthy})
	}
	for _, source := range runtime.SourceFlow.Snapshot() {
		snapshot.Equipment = append(snapshot.Equipment, model.EquipmentView{ID: string(source.Source), Kind: "source", Fence: source.Generation, Healthy: source.VideoLocked && source.AudioLocked})
	}
	sort.Slice(snapshot.Operations, func(i, j int) bool { return snapshot.Operations[i].Name < snapshot.Operations[j].Name })
	sort.Slice(snapshot.Equipment, func(i, j int) bool { return snapshot.Equipment[i].ID < snapshot.Equipment[j].ID })
	sort.Slice(snapshot.Interlocks, func(i, j int) bool {
		if snapshot.Interlocks[i].EventID == snapshot.Interlocks[j].EventID {
			return snapshot.Interlocks[i].Name < snapshot.Interlocks[j].Name
		}
		return snapshot.Interlocks[i].EventID < snapshot.Interlocks[j].EventID
	})
	return snapshot
}

func (runtime *Runtime) Operations() []model.OperationView {
	return runtime.Snapshot().Operations
}

func (runtime *Runtime) Equipment() []model.EquipmentView {
	return runtime.Snapshot().Equipment
}

func (runtime *Runtime) Interlocks() []model.InterlockView {
	return runtime.Snapshot().Interlocks
}

func (runtime *Runtime) Incidents() []model.IncidentView {
	return runtime.Snapshot().Incidents
}
