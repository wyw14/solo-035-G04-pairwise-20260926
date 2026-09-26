package service

import (
	"fmt"
	"time"

	"github.com/wyw14/solo-035/internal/model"
)

func Bootstrap(runtime *Runtime) error {
	coordinator := NewCoordinator(runtime)
	event, err := coordinator.CreateEvent("Evening News")
	if err != nil {
		return err
	}
	sourceID := model.NewSourceID()
	status, err := coordinator.PrepareSignal(event.ID, sourceID)
	if err != nil {
		return err
	}
	if err := coordinator.ConfirmSignal(sourceID, status.CurrentCommand); err != nil {
		return err
	}
	base := runtime.Clock().UTC()
	for sequence := int64(1); sequence <= 3; sequence++ {
		frame := model.Frame{
			Source:           sourceID,
			SourceGeneration: status.Generation,
			Sequence:         sequence,
			Timecode:         1000 + sequence,
			CapturedAt:       base.Add(time.Duration(sequence) * 20 * time.Millisecond),
			VideoLocked:      true,
			AudioLocked:      true,
		}
		if err := coordinator.PushFrame(event.ID, frame); err != nil {
			return err
		}
	}
	if err := coordinator.ConfigureProgram(event.ID, sourceID); err != nil {
		return err
	}
	if err := coordinator.EstablishInterlocks(event.ID); err != nil {
		return err
	}
	output, ok := runtime.Playout.Output(event.ID)
	if !ok {
		return fmt.Errorf("bootstrap output was not staged")
	}
	if err := coordinator.CommitOnAir(event.ID, output.Fence); err != nil {
		return err
	}
	// International signal: the original-language track defines the
	// reference timeline and the English track is translated against it.
	languages := []string{"zh-CN", "en"}
	texts := map[string][]string{
		"zh-CN": {"晚上好", "欢迎收看新闻", "稍后回来"},
		"en":    {"Good evening", "Welcome to the news", "We will be right back"},
	}
	for _, language := range languages {
		if _, err := runtime.RegisterSubtitleLanguage(event.ID, language); err != nil {
			return err
		}
		for index, text := range texts[language] {
			start := int64(index * 3000)
			cue, err := runtime.SubmitSubtitleCue(event.ID, language, "", text, start, start+3000, event.Revision)
			if err != nil {
				return err
			}
			if _, err := runtime.ReviewSubtitleCue(event.ID, language, cue.ID); err != nil {
				return err
			}
		}
	}
	return nil
}
