//go:build integration

package integration_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"

	"creatorinsight/backend-go/internal/platform/messaging"

	"github.com/jmoiron/sqlx"
	"github.com/nats-io/nats.go"
)

func TestPoisonEventReachesDLQAndSucceedsAfterReplay(t *testing.T) {
	natsURL := os.Getenv("NATS_URL")
	if natsURL == "" {
		natsURL = "nats://127.0.0.1:14222"
	}
	connection, err := nats.Connect(natsURL, nats.Timeout(5*time.Second))
	if err != nil {
		t.Skipf("NATS system test unavailable: %v", err)
	}
	defer connection.Close()
	jetStream, err := connection.JetStream()
	if err != nil {
		t.Fatal(err)
	}
	systemDB, err := sqlx.Connect("pgx", systemDSN)
	if err != nil {
		t.Fatal(err)
	}
	defer systemDB.Close()

	unique := time.Now().UnixNano()
	eventID := fmt.Sprintf("integration-poison-%d", unique)
	eventType := fmt.Sprintf("integration.poison_%d", unique)
	userID := int64(8_000_000_000_000 + unique%1_000_000)
	subject := "noteinsight.events." + eventType
	dlqSubject := "noteinsight.dlq." + eventType
	payload, _ := json.Marshal(map[string]any{"project_id": 1, "user_id": userID})
	envelope := messaging.EventEnvelope{
		EventID:       eventID,
		EventType:     eventType,
		AggregateType: "integration",
		AggregateID:   userID,
		SchemaVersion: 1,
		Producer:      "integration-test",
		Payload:       payload,
		OccurredAt:    time.Now().UTC(),
	}
	raw, _ := json.Marshal(envelope)
	if _, err := jetStream.Publish(subject, raw, nats.MsgId(eventID)); err != nil {
		t.Fatal(err)
	}

	var deadLetter messaging.DeadLetterEnvelope
	waitForSystemCondition(t, 45*time.Second, func() bool {
		message, err := jetStream.GetLastMsg("NOTEINSIGHT_DLQ", dlqSubject)
		if err != nil || json.Unmarshal(message.Data, &deadLetter) != nil {
			return false
		}
		return deadLetter.EventID == eventID && deadLetter.Deliveries >= 5
	}, "poison event to enter DLQ after five deliveries")

	username := fmt.Sprintf("dlq_replay_%d", unique)
	if _, err := systemDB.Exec(`
INSERT INTO users (id, username, nickname, role, status, created_at, updated_at)
VALUES ($1, $2, 'DLQ Replay Integration', 'normal', 'active', now(), now())`, userID, username); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = systemDB.Exec(`DELETE FROM processed_events WHERE event_id=$1`, eventID)
		_, _ = systemDB.Exec(`DELETE FROM users WHERE id=$1`, userID)
	}()
	if _, err := jetStream.Publish(deadLetter.OriginalSubject, deadLetter.OriginalMessage, nats.MsgId("replay_"+eventID)); err != nil {
		t.Fatal(err)
	}
	waitForSystemCondition(t, 30*time.Second, func() bool {
		var count int
		err := systemDB.QueryRow(`SELECT COUNT(*) FROM processed_events WHERE event_id=$1`, eventID).Scan(&count)
		return err == nil && count == 1
	}, "replayed event to be processed exactly once")
}

func waitForSystemCondition(t *testing.T, timeout time.Duration, condition func() bool, description string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		if condition() {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf("timed out waiting for %s", description)
		case <-ticker.C:
		}
	}
}
