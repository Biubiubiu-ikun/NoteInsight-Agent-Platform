package note

import (
	"encoding/base64"
	"encoding/json"
	"time"
)

type keysetCursor struct {
	CreatedAt time.Time `json:"created_at"`
	ID        int64     `json:"id"`
}

func encodeNoteCursor(item Note) (string, error) {
	return encodeCursor(keysetCursor{CreatedAt: item.CreatedAt, ID: item.ID})
}

func decodeNoteCursor(value string) (keysetCursor, error) {
	return decodeCursor(value)
}

func encodeCommentCursor(item NoteComment) (string, error) {
	return encodeCursor(keysetCursor{CreatedAt: item.CreatedAt, ID: item.ID})
}

func decodeCommentCursor(value string) (keysetCursor, error) {
	return decodeCursor(value)
}

func encodeCursor(cursor keysetCursor) (string, error) {
	payload, err := json.Marshal(cursor)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(payload), nil
}

func decodeCursor(value string) (keysetCursor, error) {
	if value == "" {
		return keysetCursor{}, nil
	}

	payload, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return keysetCursor{}, ValidationError{Field: "cursor", Message: "must be a valid pagination cursor"}
	}

	var cursor keysetCursor
	if err := json.Unmarshal(payload, &cursor); err != nil {
		return keysetCursor{}, ValidationError{Field: "cursor", Message: "must be a valid pagination cursor"}
	}
	if cursor.CreatedAt.IsZero() || cursor.ID <= 0 {
		return keysetCursor{}, ValidationError{Field: "cursor", Message: "must include created_at and id"}
	}
	return cursor, nil
}
