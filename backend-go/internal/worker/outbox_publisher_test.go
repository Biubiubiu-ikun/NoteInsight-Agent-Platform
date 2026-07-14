package worker

import (
	"context"
	"errors"
	"testing"
	"time"

	"creatorinsight/backend-go/internal/outbox"
)

type fakeOutboxRepository struct {
	events     []outbox.Event
	sent       []int64
	retried    []int64
	failed     []int64
	retryCount int
	counts     map[string]int64
}

func (f *fakeOutboxRepository) LockPending(context.Context, int) ([]outbox.Event, error) {
	return f.events, nil
}

func (f *fakeOutboxRepository) MarkSent(_ context.Context, id int64) error {
	f.sent = append(f.sent, id)
	return nil
}

func (f *fakeOutboxRepository) MarkRetry(_ context.Context, id int64, retryCount int, _ time.Time, _ string) error {
	f.retried = append(f.retried, id)
	f.retryCount = retryCount
	return nil
}

func (f *fakeOutboxRepository) MarkFailed(_ context.Context, id int64, retryCount int, _ string) error {
	f.failed = append(f.failed, id)
	f.retryCount = retryCount
	return nil
}

func (f *fakeOutboxRepository) RecoverStaleProcessing(context.Context, time.Time) (int64, error) {
	return 0, nil
}

func (f *fakeOutboxRepository) CountByStatus(context.Context) (map[string]int64, error) {
	return f.counts, nil
}

func (f *fakeOutboxRepository) OldestUnsentAge(context.Context) (time.Duration, error) {
	return 0, nil
}

type fakeEventPublisher struct {
	events []outbox.Event
	err    error
}

func (f *fakeEventPublisher) PublishEvent(_ context.Context, event outbox.Event) error {
	f.events = append(f.events, event)
	return f.err
}

func TestOutboxPublisherMarksSentAfterBrokerAck(t *testing.T) {
	repo := &fakeOutboxRepository{events: []outbox.Event{{ID: 7, EventID: "evt_7"}}}
	broker := &fakeEventPublisher{}
	publisher := NewOutboxPublisher(OutboxPublisherDeps{Repository: repo, Publisher: broker})

	if err := publisher.processBatch(context.Background()); err != nil {
		t.Fatalf("processBatch() error = %v", err)
	}
	if len(broker.events) != 1 || len(repo.sent) != 1 || repo.sent[0] != 7 {
		t.Fatalf("publish=%d sent=%v", len(broker.events), repo.sent)
	}
}

func TestOutboxPublisherSchedulesRetry(t *testing.T) {
	repo := &fakeOutboxRepository{events: []outbox.Event{{ID: 9, EventID: "evt_9", RetryCount: 2}}}
	broker := &fakeEventPublisher{err: errors.New("NATS down")}
	publisher := NewOutboxPublisher(OutboxPublisherDeps{Repository: repo, Publisher: broker, MaxRetries: 5})

	if err := publisher.processBatch(context.Background()); err != nil {
		t.Fatalf("processBatch() error = %v", err)
	}
	if len(repo.retried) != 1 || repo.retryCount != 3 || len(repo.sent) != 0 {
		t.Fatalf("retried=%v retry_count=%d sent=%v", repo.retried, repo.retryCount, repo.sent)
	}
}

func TestOutboxPublisherMarksFailedAtRetryLimit(t *testing.T) {
	repo := &fakeOutboxRepository{events: []outbox.Event{{ID: 11, EventID: "evt_11", RetryCount: 4}}}
	publisher := NewOutboxPublisher(OutboxPublisherDeps{
		Repository: repo,
		Publisher:  &fakeEventPublisher{err: errors.New("NATS down")},
		MaxRetries: 5,
	})

	if err := publisher.processBatch(context.Background()); err != nil {
		t.Fatalf("processBatch() error = %v", err)
	}
	if len(repo.failed) != 1 || repo.retryCount != 5 {
		t.Fatalf("failed=%v retry_count=%d", repo.failed, repo.retryCount)
	}
}
