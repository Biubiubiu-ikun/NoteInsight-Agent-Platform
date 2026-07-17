package evidence

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

type citationSegment struct {
	Source            DocumentSource
	DocumentStartByte int
	DocumentEndByte   int
	SourceStartByte   int
}

type sourceTimestamps struct {
	CreatedAt *time.Time `json:"created_at"`
	UpdatedAt *time.Time `json:"updated_at"`
}

type commentPayload struct {
	NoteID    int64  `json:"note_id"`
	Sentiment string `json:"sentiment"`
	Intent    string `json:"intent"`
	TopicID   int64  `json:"topic_id"`
}

type factPayload struct {
	NoteID           int64  `json:"note_id"`
	UserID           int64  `json:"user_id"`
	SourceRunID      string `json:"source_run_id"`
	ViewCount        int64  `json:"view_count"`
	LikeCount        int64  `json:"like_count"`
	CollectCount     int64  `json:"collect_count"`
	CommentCount     int64  `json:"comment_count"`
	ShareCount       int64  `json:"share_count"`
	UniqueUserCount  int64  `json:"unique_user_count"`
	InteractionCount int64  `json:"interaction_count"`
	ContentCount     int64  `json:"content_count"`
	ActiveNoteCount  int64  `json:"active_note_count"`
	EventCount       int64  `json:"event_count"`
}

func BuildSourceDocuments(run Run, sources []SourceInput) ([]DocumentInput, error) {
	documents := make([]DocumentInput, 0, len(sources))
	commentsByNote := make(map[int64][]SourceInput)
	for _, source := range sources {
		if source.SourceType == "note_comment" {
			var payload commentPayload
			if err := json.Unmarshal(source.SourcePayload, &payload); err != nil {
				return nil, fmt.Errorf("decode comment source %d payload: %w", source.SourceID, err)
			}
			if payload.NoteID <= 0 {
				return nil, fmt.Errorf("comment source %d has no note_id", source.SourceID)
			}
			commentsByNote[payload.NoteID] = append(commentsByNote[payload.NoteID], source)
			continue
		}
		if source.SourceType != "note" && source.SourceType != "note_media" {
			return nil, fmt.Errorf("unsupported snapshot source type %q", source.SourceType)
		}
		document, ok, err := buildDirectDocument(run, source)
		if err != nil {
			return nil, err
		}
		if ok {
			documents = append(documents, document)
		}
	}

	noteIDs := make([]int64, 0, len(commentsByNote))
	for noteID := range commentsByNote {
		noteIDs = append(noteIDs, noteID)
	}
	sort.Slice(noteIDs, func(i int, j int) bool { return noteIDs[i] < noteIDs[j] })
	for _, noteID := range noteIDs {
		clusters, err := buildCommentClusters(run, noteID, commentsByNote[noteID])
		if err != nil {
			return nil, err
		}
		documents = append(documents, clusters...)
	}
	return documents, nil
}

func BuildFactDocuments(run Run, facts []FactInput) ([]DocumentInput, error) {
	documents := make([]DocumentInput, 0, len(facts))
	for _, fact := range facts {
		var payload factPayload
		if err := json.Unmarshal(fact.SourcePayload, &payload); err != nil {
			return nil, fmt.Errorf("decode daily fact payload %d: %w", fact.DailyFactPayloadID, err)
		}
		canonical, err := canonicalFactText(fact, payload)
		if err != nil {
			return nil, err
		}
		factID := fact.DailyFactPayloadID
		source := DocumentSource{
			DailyFactPayloadID: &factID,
			SourceType:         fact.FactType,
			SourceID:           fact.SubjectID,
			SourceVersion:      fact.SourceVersion,
			ContentHash:        fact.ContentHash,
			CanonicalText:      canonical,
		}
		sourceID := fact.SubjectID
		sourceKey := fmt.Sprintf("%s:%d:%s", fact.FactType, fact.SubjectID, fact.FactDate.UTC().Format("2006-01-02"))
		metadata, _ := json.Marshal(map[string]any{
			"fact_date":     fact.FactDate.UTC().Format("2006-01-02"),
			"source_run_id": payload.SourceRunID,
			"payload_id":    fact.DailyFactPayloadID,
		})
		document, err := buildDocument(documentSpec{
			Run:               run,
			DocumentType:      fact.FactType,
			SourceType:        fact.FactType,
			SourceID:          &sourceID,
			SourceKey:         sourceKey,
			SourceVersion:     fact.SourceVersion,
			SourceContentHash: fact.ContentHash,
			Visibility:        "project",
			CanonicalText:     canonical,
			Metadata:          metadata,
			SourceCreatedAt:   &fact.CapturedAt,
			SourceUpdatedAt:   &fact.CapturedAt,
			Sources:           []DocumentSource{source},
			Segments: []citationSegment{{
				Source:            source,
				DocumentStartByte: 0,
				DocumentEndByte:   len([]byte(canonical)),
			}},
		})
		if err != nil {
			return nil, err
		}
		documents = append(documents, document)
	}
	return documents, nil
}

func buildDirectDocument(run Run, source SourceInput) (DocumentInput, bool, error) {
	canonical := NormalizeLineEndings(source.CanonicalText)
	if canonical == "" {
		return DocumentInput{}, false, nil
	}
	sourceID := source.SourceID
	evidenceSourceID := source.EvidenceSourceID
	documentSource := DocumentSource{
		EvidenceSourceID: &evidenceSourceID,
		SourceType:       source.SourceType,
		SourceID:         source.SourceID,
		SourceVersion:    source.SourceVersion,
		ContentHash:      source.ContentHash,
		CanonicalText:    canonical,
	}
	var timestamps sourceTimestamps
	_ = json.Unmarshal(source.SourcePayload, &timestamps)
	metadata, _ := json.Marshal(map[string]any{"evidence_source_id": source.EvidenceSourceID})
	document, err := buildDocument(documentSpec{
		Run:               run,
		DocumentType:      source.SourceType,
		SourceType:        source.SourceType,
		SourceID:          &sourceID,
		SourceKey:         fmt.Sprintf("%s:%d", source.SourceType, source.SourceID),
		SourceVersion:     source.SourceVersion,
		SourceContentHash: source.ContentHash,
		Visibility:        source.Visibility,
		CanonicalText:     canonical,
		Metadata:          metadata,
		SourceCreatedAt:   timestamps.CreatedAt,
		SourceUpdatedAt:   timestamps.UpdatedAt,
		Sources:           []DocumentSource{documentSource},
		Segments: []citationSegment{{
			Source:            documentSource,
			DocumentStartByte: 0,
			DocumentEndByte:   len([]byte(canonical)),
		}},
	})
	return document, true, err
}

func buildCommentClusters(run Run, noteID int64, sources []SourceInput) ([]DocumentInput, error) {
	sort.Slice(sources, func(i int, j int) bool {
		left := commentValue(sources[i])
		right := commentValue(sources[j])
		if left != right {
			return left > right
		}
		return sources[i].SourceID < sources[j].SourceID
	})
	documents := make([]DocumentInput, 0, (len(sources)+CommentClusterSize-1)/CommentClusterSize)
	for start, clusterIndex := 0, 0; start < len(sources); start, clusterIndex = start+CommentClusterSize, clusterIndex+1 {
		end := start + CommentClusterSize
		if end > len(sources) {
			end = len(sources)
		}
		members := sources[start:end]
		var text strings.Builder
		segments := make([]citationSegment, 0, len(members))
		documentSources := make([]DocumentSource, 0, len(members))
		hashInput := make([]string, 0, len(members)*3)
		visibility := "public"
		for index, member := range members {
			canonical := NormalizeLineEndings(member.CanonicalText)
			if canonical == "" {
				continue
			}
			if text.Len() > 0 {
				text.WriteString("\n\n")
			}
			_, _ = fmt.Fprintf(&text, "[comment %d]\n", member.SourceID)
			documentStart := text.Len()
			text.WriteString(canonical)
			evidenceSourceID := member.EvidenceSourceID
			documentSource := DocumentSource{
				EvidenceSourceID: &evidenceSourceID,
				SourceType:       member.SourceType,
				SourceID:         member.SourceID,
				SourceVersion:    member.SourceVersion,
				ContentHash:      member.ContentHash,
				SourceOrder:      len(documentSources),
				CanonicalText:    canonical,
			}
			documentSources = append(documentSources, documentSource)
			segments = append(segments, citationSegment{
				Source:            documentSource,
				DocumentStartByte: documentStart,
				DocumentEndByte:   text.Len(),
			})
			hashInput = append(hashInput, fmt.Sprintf("%d", member.SourceID), fmt.Sprintf("%d", member.SourceVersion), member.ContentHash)
			if member.Visibility == "project" {
				visibility = "project"
			}
			_ = index
		}
		if len(documentSources) == 0 {
			continue
		}
		noteIDCopy := noteID
		sourceKey := fmt.Sprintf("note:%d:comment-cluster:%04d", noteID, clusterIndex)
		metadata, _ := json.Marshal(map[string]any{
			"note_id":        noteID,
			"cluster_index":  clusterIndex,
			"member_count":   len(documentSources),
			"ranking_method": "semantic_metadata_then_length_v1",
		})
		document, err := buildDocument(documentSpec{
			Run:               run,
			DocumentType:      "note_comment_cluster",
			SourceType:        "note_comment_cluster",
			SourceID:          &noteIDCopy,
			SourceKey:         sourceKey,
			SourceVersion:     run.DatasetVersion,
			SourceContentHash: hashParts(hashInput...),
			Visibility:        visibility,
			CanonicalText:     text.String(),
			Metadata:          metadata,
			Sources:           documentSources,
			Segments:          segments,
		})
		if err != nil {
			return nil, err
		}
		documents = append(documents, document)
	}
	return documents, nil
}

func commentValue(source SourceInput) int {
	var payload commentPayload
	_ = json.Unmarshal(source.SourcePayload, &payload)
	value := utf8.RuneCountInString(source.CanonicalText)
	if payload.Intent != "" {
		value += 80
	}
	if payload.Sentiment != "" {
		value += 40
	}
	if payload.TopicID > 0 {
		value += 20
	}
	return value
}

func canonicalFactText(fact FactInput, payload factPayload) (string, error) {
	date := fact.FactDate.UTC().Format("2006-01-02")
	switch fact.FactType {
	case "note_daily_fact":
		return fmt.Sprintf(
			"笔记日事实\n日期: %s\n笔记ID: %d\n浏览: %d\n点赞: %d\n收藏: %d\n评论: %d\n分享: %d\n独立用户: %d\n事件总数: %d",
			date, fact.SubjectID, payload.ViewCount, payload.LikeCount, payload.CollectCount,
			payload.CommentCount, payload.ShareCount, payload.UniqueUserCount, payload.EventCount,
		), nil
	case "user_daily_fact":
		return fmt.Sprintf(
			"用户日事实\n日期: %s\n用户ID: %d\n浏览: %d\n互动: %d\n发帖: %d\n评论: %d\n活跃笔记: %d\n事件总数: %d",
			date, fact.SubjectID, payload.ViewCount, payload.InteractionCount, payload.ContentCount,
			payload.CommentCount, payload.ActiveNoteCount, payload.EventCount,
		), nil
	default:
		return "", fmt.Errorf("unsupported daily fact type %q", fact.FactType)
	}
}

type documentSpec struct {
	Run               Run
	DocumentType      string
	SourceType        string
	SourceID          *int64
	SourceKey         string
	SourceVersion     int64
	SourceContentHash string
	Visibility        string
	CanonicalText     string
	Metadata          json.RawMessage
	SourceCreatedAt   *time.Time
	SourceUpdatedAt   *time.Time
	Sources           []DocumentSource
	Segments          []citationSegment
}

func buildDocument(spec documentSpec) (DocumentInput, error) {
	canonical := NormalizeLineEndings(spec.CanonicalText)
	ranges, err := splitCanonicalText(canonical, MaxChunkBytes, ChunkOverlap)
	if err != nil {
		return DocumentInput{}, err
	}
	if len(ranges) == 0 {
		return DocumentInput{}, fmt.Errorf("document %s has empty canonical text", spec.SourceKey)
	}
	contentHash := ContractHash(ParserVersion, canonical)
	documentKey := hashParts(
		fmt.Sprintf("%d", spec.Run.DatasetVersionID), spec.DocumentType, spec.SourceKey,
		fmt.Sprintf("%d", spec.SourceVersion), spec.SourceContentHash, ParserVersion, contentHash,
	)
	document := DocumentInput{
		DocumentKey:       documentKey,
		ProjectID:         spec.Run.ProjectID,
		DatasetID:         spec.Run.DatasetID,
		DatasetVersionID:  spec.Run.DatasetVersionID,
		DocumentType:      spec.DocumentType,
		SourceType:        spec.SourceType,
		SourceID:          spec.SourceID,
		SourceKey:         spec.SourceKey,
		SourceVersion:     spec.SourceVersion,
		SourceContentHash: spec.SourceContentHash,
		ParserVersion:     ParserVersion,
		Visibility:        spec.Visibility,
		CanonicalText:     canonical,
		ContentHash:       contentHash,
		Metadata:          spec.Metadata,
		SourceCreatedAt:   spec.SourceCreatedAt,
		SourceUpdatedAt:   spec.SourceUpdatedAt,
		Sources:           spec.Sources,
		Chunks:            make([]ChunkInput, 0, len(ranges)),
	}
	for index, current := range ranges {
		chunkHash := hashParts(current.Content)
		chunkKey := hashParts(documentKey, fmt.Sprintf("%d", index), fmt.Sprintf("%d", current.StartByte), fmt.Sprintf("%d", current.EndByte), chunkHash, ChunkerVersion)
		tokens := Tokenize(current.Content)
		chunkMetadata, _ := json.Marshal(map[string]any{"token_count": len(tokens)})
		chunk := ChunkInput{
			ChunkKey:         chunkKey,
			ChunkIndex:       index,
			StartByte:        current.StartByte,
			EndByte:          current.EndByte,
			StartRune:        current.StartRune,
			EndRune:          current.EndRune,
			Content:          current.Content,
			ContentHash:      chunkHash,
			ChunkerVersion:   ChunkerVersion,
			TokenizerVersion: TokenizerVersion,
			Lexemes:          strings.Join(tokens, " "),
			Metadata:         chunkMetadata,
		}
		for _, segment := range spec.Segments {
			intersectionStart := max(current.StartByte, segment.DocumentStartByte)
			intersectionEnd := min(current.EndByte, segment.DocumentEndByte)
			if intersectionStart >= intersectionEnd {
				continue
			}
			sourceStart := segment.SourceStartByte + intersectionStart - segment.DocumentStartByte
			sourceEnd := sourceStart + intersectionEnd - intersectionStart
			sourceBytes := []byte(segment.Source.CanonicalText)
			if sourceStart < 0 || sourceEnd > len(sourceBytes) || !utf8.Valid(sourceBytes[sourceStart:sourceEnd]) {
				return DocumentInput{}, fmt.Errorf("citation for %s/%d has invalid source range [%d,%d)", segment.Source.SourceType, segment.Source.SourceID, sourceStart, sourceEnd)
			}
			quoteHash := hashParts(string(sourceBytes[sourceStart:sourceEnd]))
			citationKey := hashParts(
				chunkKey, segment.Source.SourceType, fmt.Sprintf("%d", segment.Source.SourceID),
				fmt.Sprintf("%d", segment.Source.SourceVersion), fmt.Sprintf("%d", intersectionStart),
				fmt.Sprintf("%d", intersectionEnd), fmt.Sprintf("%d", sourceStart), fmt.Sprintf("%d", sourceEnd), quoteHash,
			)
			chunk.Citations = append(chunk.Citations, CitationInput{
				CitationKey:        citationKey,
				EvidenceSourceID:   segment.Source.EvidenceSourceID,
				DailyFactPayloadID: segment.Source.DailyFactPayloadID,
				SourceType:         segment.Source.SourceType,
				SourceID:           segment.Source.SourceID,
				SourceVersion:      segment.Source.SourceVersion,
				SourceContentHash:  segment.Source.ContentHash,
				DocumentStartByte:  intersectionStart,
				DocumentEndByte:    intersectionEnd,
				SourceStartByte:    sourceStart,
				SourceEndByte:      sourceEnd,
				QuoteHash:          quoteHash,
			})
		}
		if len(chunk.Citations) == 0 {
			return DocumentInput{}, fmt.Errorf("chunk %s has no source citation", chunkKey)
		}
		document.Chunks = append(document.Chunks, chunk)
	}
	return document, nil
}
