package simulator

import "context"

type DiscardSink struct{}

func (DiscardSink) WriteProfiles(context.Context, []UserProfile) error { return nil }
func (DiscardSink) WriteEvent(context.Context, Event) error            { return nil }
func (DiscardSink) Complete(context.Context, Report) error             { return nil }
func (DiscardSink) Abort(context.Context, error)                       {}

type MultiSink struct {
	sinks []Sink
}

func NewMultiSink(sinks ...Sink) Sink {
	filtered := make([]Sink, 0, len(sinks))
	for _, sink := range sinks {
		if sink != nil {
			filtered = append(filtered, sink)
		}
	}
	if len(filtered) == 0 {
		return DiscardSink{}
	}
	if len(filtered) == 1 {
		return filtered[0]
	}
	return &MultiSink{sinks: filtered}
}

func (s *MultiSink) WriteProfiles(ctx context.Context, profiles []UserProfile) error {
	for _, sink := range s.sinks {
		if err := sink.WriteProfiles(ctx, profiles); err != nil {
			return err
		}
	}
	return nil
}

func (s *MultiSink) WriteEvent(ctx context.Context, event Event) error {
	for _, sink := range s.sinks {
		if err := sink.WriteEvent(ctx, event); err != nil {
			return err
		}
	}
	return nil
}

func (s *MultiSink) Complete(ctx context.Context, report Report) error {
	for _, sink := range s.sinks {
		if err := sink.Complete(ctx, report); err != nil {
			return err
		}
	}
	return nil
}

func (s *MultiSink) Abort(ctx context.Context, cause error) {
	for _, sink := range s.sinks {
		sink.Abort(ctx, cause)
	}
}
