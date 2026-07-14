package simulator

import (
	"context"
	"reflect"
	"testing"
)

type memorySink struct {
	profiles []UserProfile
	events   []Event
	report   Report
	aborted  bool
}

func (s *memorySink) WriteProfiles(_ context.Context, profiles []UserProfile) error {
	s.profiles = append(s.profiles, profiles...)
	return nil
}

func (s *memorySink) WriteEvent(_ context.Context, event Event) error {
	s.events = append(s.events, event)
	return nil
}

func (s *memorySink) Complete(_ context.Context, report Report) error {
	s.report = report
	return nil
}

func (s *memorySink) Abort(context.Context, error) {
	s.aborted = true
}

func TestEngineIsDeterministicForFixedSeed(t *testing.T) {
	preset, err := PresetFor("smoke", 42, ScenarioMixed)
	if err != nil {
		t.Fatal(err)
	}
	preset.Config.Sessions = 120
	dataset := SyntheticDataset(25, 40, 3)
	first := &memorySink{}
	second := &memorySink{}

	firstReport, err := NewEngine().Generate(context.Background(), preset.Config, dataset, first)
	if err != nil {
		t.Fatalf("first Generate() error = %v", err)
	}
	secondReport, err := NewEngine().Generate(context.Background(), preset.Config, dataset, second)
	if err != nil {
		t.Fatalf("second Generate() error = %v", err)
	}

	if !reflect.DeepEqual(first.profiles, second.profiles) {
		t.Fatal("profiles differ for the same seed")
	}
	if !reflect.DeepEqual(first.events, second.events) {
		t.Fatal("events differ for the same seed")
	}
	if !reflect.DeepEqual(firstReport, secondReport) {
		t.Fatal("reports differ for the same seed")
	}
}

func TestEngineProducesValidSessionChains(t *testing.T) {
	preset, _ := PresetFor("smoke", 2026, ScenarioMixed)
	preset.Config.Sessions = 200
	sink := &memorySink{}
	report, err := NewEngine().Generate(context.Background(), preset.Config, SyntheticDataset(40, 80, 4), sink)
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}

	type sessionState struct {
		nextSequence int
		first        string
		last         string
	}
	sessions := map[string]*sessionState{}
	eventIDs := map[string]struct{}{}
	for _, event := range sink.events {
		state := sessions[event.SessionID]
		if state == nil {
			state = &sessionState{nextSequence: 1}
			sessions[event.SessionID] = state
		}
		if event.SequenceNo != state.nextSequence {
			t.Fatalf("session %s sequence=%d want=%d", event.SessionID, event.SequenceNo, state.nextSequence)
		}
		if state.first == "" {
			state.first = event.EventType
		}
		state.last = event.EventType
		state.nextSequence++
		if _, exists := eventIDs[event.SourceEventID]; exists {
			t.Fatalf("duplicate source_event_id %s", event.SourceEventID)
		}
		eventIDs[event.SourceEventID] = struct{}{}
		if event.EventType == string(StateCommentLiked) && event.CommentID <= 0 {
			t.Fatal("comment_liked event is missing comment_id")
		}
	}
	if len(sessions) != preset.Config.Sessions {
		t.Fatalf("sessions=%d want=%d", len(sessions), preset.Config.Sessions)
	}
	for sessionID, state := range sessions {
		if state.first != string(StateFeedImpression) || state.last != string(StateSessionExited) {
			t.Fatalf("session %s first=%s last=%s", sessionID, state.first, state.last)
		}
	}
	if int64(len(sink.events)) != report.Events {
		t.Fatalf("events=%d report=%d", len(sink.events), report.Events)
	}
}

func TestSmokeDistributionQualityChecksPass(t *testing.T) {
	preset, _ := PresetFor("smoke", 20260714, ScenarioMixed)
	report, err := NewEngine().Generate(context.Background(), preset.Config, SyntheticDataset(preset.UserLimit, preset.NoteLimit, 5), DiscardSink{})
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	if len(report.PersonaCounts) < 6 {
		t.Fatalf("persona diversity=%d", len(report.PersonaCounts))
	}
	for _, check := range report.Checks {
		if !check.Passed {
			t.Fatalf("distribution check %s failed: value=%f target=%s", check.Name, check.Value, check.Target)
		}
	}
}

func TestOrganicScenarioHasNoBurstEvents(t *testing.T) {
	preset, _ := PresetFor("smoke", 99, ScenarioOrganic)
	preset.Config.Sessions = 100
	report, err := NewEngine().Generate(context.Background(), preset.Config, SyntheticDataset(20, 30, 2), DiscardSink{})
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	if report.BurstEventRatio != 0 {
		t.Fatalf("burst ratio=%f want=0", report.BurstEventRatio)
	}
}

func TestConfigRejectsUnsafeRunID(t *testing.T) {
	preset, _ := PresetFor("smoke", 1, ScenarioMixed)
	preset.Config.RunID = "../unsafe"
	if err := preset.Config.Validate(); err == nil {
		t.Fatal("Validate() expected unsafe run_id error")
	}
}

func BenchmarkEngineTenThousandSessions(b *testing.B) {
	preset, _ := PresetFor("dev", 42, ScenarioMixed)
	preset.Config.Sessions = 10_000
	dataset := SyntheticDataset(1_000, 5_000, 5)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		preset.Config.Seed = int64(42 + i)
		if _, err := NewEngine().Generate(context.Background(), preset.Config, dataset, DiscardSink{}); err != nil {
			b.Fatal(err)
		}
	}
}
