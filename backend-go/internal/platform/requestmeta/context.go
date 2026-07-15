package requestmeta

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
)

type metadataKey struct{}

type Metadata struct {
	RequestID string
	TraceID   string
}

func With(ctx context.Context, metadata Metadata) context.Context {
	metadata.RequestID = strings.TrimSpace(metadata.RequestID)
	metadata.TraceID = strings.TrimSpace(metadata.TraceID)
	return context.WithValue(ctx, metadataKey{}, metadata)
}

func From(ctx context.Context) Metadata {
	metadata, _ := ctx.Value(metadataKey{}).(Metadata)
	return metadata
}

func NewID(prefix string) string {
	var suffix [12]byte
	if _, err := rand.Read(suffix[:]); err == nil {
		return fmt.Sprintf("%s_%s", prefix, hex.EncodeToString(suffix[:]))
	}
	return fmt.Sprintf("%s_%d", prefix, time.Now().UnixNano())
}
