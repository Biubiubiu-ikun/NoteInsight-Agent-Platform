package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"creatorinsight/backend-go/internal/config"
	"creatorinsight/backend-go/internal/platform/messaging"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

func main() {
	limit := flag.Int("limit", 20, "maximum DLQ messages to inspect")
	eventID := flag.String("event-id", "", "only inspect or replay one event id")
	replay := flag.Bool("replay", false, "republish matching original messages")
	flag.Parse()
	if *limit < 1 || *limit > 1000 {
		slog.Error("limit must be between 1 and 1000")
		os.Exit(2)
	}

	cfg, err := config.Load()
	if err != nil {
		slog.Error("load config failed", "error", err)
		os.Exit(1)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	connection, err := nats.Connect(cfg.NATS.URL, nats.Name("noteinsight-dlqctl"), nats.Timeout(cfg.NATS.ConnectTimeout))
	if err != nil {
		slog.Error("connect NATS failed", "error", err)
		os.Exit(1)
	}
	defer connection.Close()
	js, err := jetstream.New(connection)
	if err != nil {
		slog.Error("create JetStream client failed", "error", err)
		os.Exit(1)
	}
	consumer, err := js.OrderedConsumer(ctx, cfg.NATS.DLQStream, jetstream.OrderedConsumerConfig{
		FilterSubjects: []string{cfg.NATS.DLQSubjectPrefix + ".>"},
		DeliverPolicy:  jetstream.DeliverAllPolicy,
	})
	if err != nil {
		slog.Error("open DLQ consumer failed", "error", err)
		os.Exit(1)
	}
	messages, err := consumer.Messages(jetstream.PullMaxMessages(50))
	if err != nil {
		slog.Error("open DLQ iterator failed", "error", err)
		os.Exit(1)
	}
	defer messages.Stop()

	matched := 0
	replayed := 0
	encoder := json.NewEncoder(os.Stdout)
	for matched < *limit {
		message, err := messages.Next(jetstream.NextMaxWait(750 * time.Millisecond))
		if err != nil {
			if strings.Contains(strings.ToLower(err.Error()), "timeout") {
				break
			}
			slog.Error("read DLQ message failed", "error", err)
			os.Exit(1)
		}
		var envelope messaging.DeadLetterEnvelope
		if err := json.Unmarshal(message.Data(), &envelope); err != nil {
			slog.Warn("skip malformed DLQ message", "error", err)
			continue
		}
		if *eventID != "" && envelope.EventID != *eventID {
			continue
		}
		matched++
		if err := encoder.Encode(envelope); err != nil {
			slog.Error("write DLQ output failed", "error", err)
			os.Exit(1)
		}
		if *replay {
			messageID := fmt.Sprintf("replay_%s_%d", envelope.EventID, time.Now().UnixNano())
			if _, err := js.Publish(ctx, envelope.OriginalSubject, envelope.OriginalMessage, jetstream.WithMsgID(messageID)); err != nil {
				slog.Error("replay DLQ message failed", "event_id", envelope.EventID, "error", err)
				os.Exit(1)
			}
			replayed++
		}
	}
	slog.Info("DLQ operation complete", "matched", matched, "replayed", replayed)
}
