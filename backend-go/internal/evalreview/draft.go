package evalreview

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"
)

var (
	daysPattern    = regexp.MustCompile(`做了([0-9]+)天记录`)
	recordsPattern = regexp.MustCompile(`共完成([0-9]+)次记录`)
	budgetPattern  = regexp.MustCompile(`预算约([0-9]+)元`)
	recordPattern  = regexp.MustCompile(`样本记录[ ]*([0-9]+)`)
)

type draftIndex struct {
	publicNotes    []DraftSource
	publicMedia    []DraftSource
	projectSources []DraftSource
	mediaByNote    map[int64][]DraftSource
	noteByID       map[int64]DraftSource
	notesBySubject map[string][]DraftSource
}

func VerifyDraftArtifacts(root string, datasetVersionID int64, ingestionRunID string) (DraftReport, error) {
	if datasetVersionID <= 0 || !validIdentifier(ingestionRunID) {
		return DraftReport{}, fmt.Errorf("dataset version and ingestion run are required")
	}
	reportPath := filepath.Join(root, "draft_report.json")
	raw, err := os.ReadFile(reportPath)
	if err != nil {
		return DraftReport{}, fmt.Errorf("read draft report: %w", err)
	}
	var report DraftReport
	if err := json.Unmarshal(raw, &report); err != nil {
		return DraftReport{}, fmt.Errorf("decode draft report: %w", err)
	}
	_, manifest, err := BuildMatrix()
	if err != nil {
		return DraftReport{}, err
	}
	if report.GeneratorVersion != DraftGeneratorVersion || report.Status != "model_draft_awaiting_human_review" ||
		report.DatasetVersionID != datasetVersionID || report.IngestionRunID != ingestionRunID ||
		report.CaseCount != manifest.CaseCount || report.MatrixChecksum != manifest.MatrixChecksum ||
		!report.ReviewRequired || !validRoleID(report.AuthorID) {
		return DraftReport{}, fmt.Errorf("draft report does not match the frozen review contract")
	}
	authoredPath := filepath.Join(root, "authored_cases.jsonl")
	checksum, err := FileChecksum(authoredPath)
	if err != nil {
		return DraftReport{}, err
	}
	if checksum != report.AuthoredChecksum {
		return DraftReport{}, fmt.Errorf("authored case checksum %s does not match draft report %s", checksum, report.AuthoredChecksum)
	}
	cases, err := ReadJSONLines[AuthoredCase](authoredPath)
	if err != nil {
		return DraftReport{}, err
	}
	if err := ValidateAuthoredMatrix(cases); err != nil {
		return DraftReport{}, err
	}
	for _, current := range cases {
		if current.AuthorID != report.AuthorID || current.DraftAssistance != "model_assisted" {
			return DraftReport{}, fmt.Errorf("case %s does not match draft author and assistance provenance", current.CaseID)
		}
	}
	return report, nil
}

func GenerateDrafts(slots []MatrixSlot, authorID string, datasetVersionID int64, ingestionRunID string, corpus DraftCorpus) ([]AuthoredCase, DraftReport, error) {
	if !validRoleID(authorID) || datasetVersionID <= 0 || !validIdentifier(ingestionRunID) {
		return nil, DraftReport{}, fmt.Errorf("valid author, dataset version, and ingestion run are required")
	}
	expectedSlots, manifest, err := BuildMatrix()
	if err != nil {
		return nil, DraftReport{}, err
	}
	if len(slots) != len(expectedSlots) {
		return nil, DraftReport{}, fmt.Errorf("draft matrix has %d slots; expected %d", len(slots), len(expectedSlots))
	}
	for index := range slots {
		if slots[index] != expectedSlots[index] {
			return nil, DraftReport{}, fmt.Errorf("draft matrix slot %d differs from the frozen matrix", index+1)
		}
	}

	indexed, err := indexDraftCorpus(corpus)
	if err != nil {
		return nil, DraftReport{}, err
	}
	cases := make([]AuthoredCase, 0, len(slots))
	perTaskOrdinal := map[string]int{}
	seenQueries := map[string]struct{}{}
	for _, slot := range slots {
		ordinal := perTaskOrdinal[slot.TaskType]
		var current AuthoredCase
		built := false
		for attempt := 0; attempt < len(indexed.publicNotes); attempt++ {
			candidate, buildErr := buildDraftCase(slot, ordinal+attempt*TargetCasesPerTask, authorID, datasetVersionID, ingestionRunID, indexed)
			if buildErr != nil {
				return nil, DraftReport{}, fmt.Errorf("draft %s: %w", slot.CaseID, buildErr)
			}
			if _, duplicate := seenQueries[normalizeQuery(candidate.Query)]; duplicate {
				continue
			}
			current = candidate
			built = true
			break
		}
		if !built {
			return nil, DraftReport{}, fmt.Errorf("draft %s could not produce a unique query", slot.CaseID)
		}
		cases = append(cases, current)
		seenQueries[normalizeQuery(current.Query)] = struct{}{}
		perTaskOrdinal[slot.TaskType]++
	}
	if err := ValidateAuthoredMatrix(cases); err != nil {
		return nil, DraftReport{}, fmt.Errorf("validate generated drafts: %w", err)
	}

	report := DraftReport{
		GeneratorVersion: DraftGeneratorVersion,
		Status:           "model_draft_awaiting_human_review",
		DatasetVersionID: datasetVersionID,
		IngestionRunID:   ingestionRunID,
		AuthorID:         authorID,
		CaseCount:        len(cases),
		SplitCounts:      map[string]int{},
		TaskCounts:       map[string]int{},
		MatrixChecksum:   manifest.MatrixChecksum,
		SourcePolicy:     "frozen_dataset_payload_and_completed_ingestion_citation_graph_only; no retrieval ranking or evaluation output",
		ReviewRequired:   true,
		KnownReviewRisks: []string{
			"synthetic corpus templates create semantically equivalent near-duplicates; reviewers must promote every genuinely sufficient source",
			"candidate pools are model-assisted proposals and may need source additions before final adjudication",
			"authorization cases use a real active project member and a deliberately absent non-member principal",
			"expected answers are author notes only and are hidden from blind reviewers",
		},
		CandidateCountMinimum: 1 << 30,
	}
	unique := map[string]struct{}{}
	for _, current := range cases {
		report.SplitCounts[current.Split]++
		report.TaskCounts[current.TaskType]++
		count := len(current.CandidateRefs)
		report.CandidateRefCount += count
		if count < report.CandidateCountMinimum {
			report.CandidateCountMinimum = count
		}
		if count > report.CandidateCountMaximum {
			report.CandidateCountMaximum = count
		}
		for _, ref := range current.CandidateRefs {
			unique[refKey(ref)] = struct{}{}
		}
	}
	report.UniqueCandidateCount = len(unique)
	return cases, report, nil
}

func indexDraftCorpus(corpus DraftCorpus) (draftIndex, error) {
	result := draftIndex{mediaByNote: map[int64][]DraftSource{}, noteByID: map[int64]DraftSource{}, notesBySubject: map[string][]DraftSource{}}
	for _, source := range corpus.Sources {
		switch {
		case source.Visibility == "public" && source.SourceType == "note":
			metrics := metricsOf(source)
			if source.Title == "" || source.Body == "" || len(source.Tags) < 2 ||
				!strings.Contains(source.Body, "【先说结论】") || metrics.Days <= 0 || metrics.Records <= 0 || metrics.Budget <= 0 {
				continue
			}
			result.publicNotes = append(result.publicNotes, source)
			result.noteByID[source.NoteID] = source
			result.notesBySubject[subjectOf(source)] = append(result.notesBySubject[subjectOf(source)], source)
		case source.Visibility == "public" && source.SourceType == "note_media":
			if source.OCRText == "" {
				continue
			}
			result.publicMedia = append(result.publicMedia, source)
			result.mediaByNote[source.NoteID] = append(result.mediaByNote[source.NoteID], source)
		case source.Visibility == "project":
			result.projectSources = append(result.projectSources, source)
		}
	}
	if len(result.publicNotes) < 512 || len(result.publicMedia) < TargetCasesPerTask || len(result.projectSources) < TargetCasesPerTask {
		return draftIndex{}, fmt.Errorf("draft corpus is too small: public_notes=%d public_media=%d project_sources=%d", len(result.publicNotes), len(result.publicMedia), len(result.projectSources))
	}
	for subject := range result.notesBySubject {
		sort.Slice(result.notesBySubject[subject], func(i, j int) bool {
			left, right := result.notesBySubject[subject][i], result.notesBySubject[subject][j]
			if !left.CreatedAt.Equal(right.CreatedAt) {
				return left.CreatedAt.Before(right.CreatedAt)
			}
			return left.SourceID < right.SourceID
		})
	}
	return result, nil
}

func buildDraftCase(slot MatrixSlot, ordinal int, authorID string, datasetVersionID int64, ingestionRunID string, index draftIndex) (AuthoredCase, error) {
	base := AuthoredCase{
		CaseID: slot.CaseID, AuthorID: authorID, TaskType: slot.TaskType, Split: slot.Split,
		RubricVersion: RubricVersion, DraftAssistance: "model_assisted",
		Metadata: map[string]any{
			"draft_generator_version": DraftGeneratorVersion,
			"dataset_version_id":      datasetVersionID,
			"ingestion_run_id":        ingestionRunID,
			"draft_status":            "awaiting_independent_human_review",
		},
	}
	target := pickNote(index.publicNotes, slot.CaseID, ordinal)
	pool := []DraftSource{}
	add := func(sources ...DraftSource) { pool = appendUniqueSources(pool, sources...) }

	switch slot.TaskType {
	case "semantic_paraphrase":
		metrics := metricsOf(target)
		base.Query = fmt.Sprintf("想找一篇关于%s的实测：面向%s，观察%d天、完成%d次记录，预算约%d元。作者最后保留了什么做法，又提醒了哪项边界？", subjectOf(target), audienceOf(target), metrics.Days, metrics.Records, metrics.Budget)
		base.ExpectedAnswer = conclusionOf(target)
		base.AdversarialTags = []string{"semantic_paraphrase", "compound_anchor", "near_duplicate"}
		add(target)
		add(mediaFor(index, target.NoteID, 0)...)
		add(peersFor(index, target, 2)...)
	case "typo_robustness":
		metrics := metricsOf(target)
		base.Query = fmt.Sprintf("我记得主题大概是“%s”，样夲记绿编号接近%d；那篇做了%d天、%d次记录的帖子最终结论是什么？", typoVariantDraft(subjectOf(target)), recordNumber(target), metrics.Days, metrics.Records)
		base.ExpectedAnswer = conclusionOf(target)
		base.AdversarialTags = []string{"typo", "phonetic_noise", "near_duplicate"}
		add(target)
		add(mediaFor(index, target.NoteID, 0)...)
		add(peersFor(index, target, 2)...)
	case "temporal_conflict":
		pair := temporalPair(index, target, ordinal)
		older, newer := pair[0], pair[1]
		base.Query = fmt.Sprintf("截至%s，比较“%s”的样本记录%d和样本记录%d：哪条更晚？请给出较晚记录的观察天数、记录次数、预算和最终边界，不能用较早记录覆盖。", newer.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"), subjectOf(newer), recordNumber(older), recordNumber(newer))
		metrics := metricsOf(newer)
		base.ExpectedAnswer = fmt.Sprintf("较晚的是样本记录%d（%s），观察%d天、完成%d次记录、预算约%d元。%s", recordNumber(newer), newer.CreatedAt.UTC().Format("2006-01-02"), metrics.Days, metrics.Records, metrics.Budget, conclusionOf(newer))
		base.AdversarialTags = []string{"temporal_conflict", "stale_evidence", "same_topic"}
		base.Metadata["as_of"] = newer.CreatedAt.UTC().Format("2006-01-02T15:04:05Z")
		add(older, newer)
		add(mediaFor(index, older.NoteID, 0)...)
		add(mediaFor(index, newer.NoteID, 0)...)
	case "cross_note_compare":
		peer := differentSubject(index, target, ordinal)
		leftMetrics, rightMetrics := metricsOf(target), metricsOf(peer)
		base.Query = fmt.Sprintf("请分别比较样本记录%d的“%s”和样本记录%d的“%s”：各自观察了多少天、记录多少次，最后建议及限制分别是什么？不要把两篇内容合并成一个来源。", recordNumber(target), subjectOf(target), recordNumber(peer), subjectOf(peer))
		base.ExpectedAnswer = fmt.Sprintf("样本记录%d：%d天、%d次，%s 样本记录%d：%d天、%d次，%s", recordNumber(target), leftMetrics.Days, leftMetrics.Records, conclusionOf(target), recordNumber(peer), rightMetrics.Days, rightMetrics.Records, conclusionOf(peer))
		base.AdversarialTags = []string{"cross_note_compare", "multi_source", "citation_separation"}
		base.Metadata["comparison_dimensions"] = []string{"observation_days", "record_count", "recommendation", "limitation"}
		add(target, peer)
		add(mediaFor(index, target.NoteID, 0)...)
		add(mediaFor(index, peer.NoteID, 0)...)
	case "no_relevant_document":
		code := shortCaseToken(slot.CaseID)
		base.Query = fmt.Sprintf("请查找编号为 NRD-%s 的“南极深海量子路由器维护日志”，并给出日志中的精确固件版本和维护人。", code)
		base.ExpectedAnswer = "冻结语料中没有这份日志，也没有足以回答固件版本或维护人的相关证据，应返回 no_relevant_document。"
		base.AdversarialTags = []string{"no_relevant_document", "nonexistent_identifier", "false_positive_trap"}
		base.Metadata["expected_answerability"] = "no_relevant_document"
		add(target)
		add(peersFor(index, target, 2)...)
	case "insufficient_evidence":
		metrics := metricsOf(target)
		base.Query = fmt.Sprintf("样本记录%d关于“%s”做了%d天、%d次记录。仅凭这些材料，能否证明该方法对全国所有人都具有至少五年的确定因果效果？", recordNumber(target), subjectOf(target), metrics.Days, metrics.Records)
		base.ExpectedAnswer = fmt.Sprintf("不能。现有证据只是%d天、%d次的合成个体记录，并明确保留适用条件和波动边界，无法支持全国人群、五年周期或确定因果结论。", metrics.Days, metrics.Records)
		base.AdversarialTags = []string{"insufficient_evidence", "causal_overclaim", "scope_limit"}
		base.Metadata["expected_answerability"] = "insufficient_evidence"
		add(target)
		add(mediaFor(index, target.NoteID, 0)...)
		add(peersFor(index, target, 2)...)
	case "ocr_detail":
		media := index.publicMedia[stableIndex(slot.CaseID, len(index.publicMedia))]
		base.Query = fmt.Sprintf("在样本记录%d的第%d张图里，OCR 卡片写出的图注和正文细节是什么？请按图中文字回答，不要只概括帖子主题。", media.NoteID, media.Position)
		base.ExpectedAnswer = fmt.Sprintf("图注：%s。OCR 文字：%s", media.Caption, media.OCRText)
		base.AdversarialTags = []string{"ocr_detail", "media_position", "caption_ocr"}
		add(media)
		if note, ok := index.noteByID[media.NoteID]; ok {
			add(note)
			add(mediaFor(index, media.NoteID, media.SourceID)...)
			add(peersFor(index, note, 1)...)
		}
	case "authorization_boundary":
		protected := index.projectSources[ordinal%len(index.projectSources)]
		token := aclToken(protected.Canonical)
		allowedUserID := int64(10001 + ordinal%32)
		deniedUserID := int64(999999001 + ordinal%32)
		base.Query = fmt.Sprintf("用户%d检索项目1内访问标签 %s 对应的备忘时可以看到什么；对照非项目成员%d又应得到什么结果？", allowedUserID, token, deniedUserID)
		base.ExpectedAnswer = fmt.Sprintf("用户%d作为项目1的 active member 可访问标签%s对应来源；非成员%d必须得到0条结果，且不得泄露正文、摘要或命中提示。", allowedUserID, token, deniedUserID)
		base.AdversarialTags = []string{"authorization_boundary", "tenant_isolation", "pre_filter"}
		base.Metadata["required_project_id"] = int64(1)
		base.Metadata["allowed_user_id"] = allowedUserID
		base.Metadata["denied_user_id"] = deniedUserID
		base.Metadata["authorized_expected_results"] = 1
		base.Metadata["unauthorized_expected_results"] = 0
		base.Metadata["denied_principal_basis"] = "deliberately_absent_from_project_members"
		add(protected)
		for offset := 1; len(pool) < 5; offset++ {
			add(index.projectSources[(ordinal+offset*7)%len(index.projectSources)])
		}
	case "out_of_domain_noise":
		code := shortCaseToken(slot.CaseID)
		questions := []string{
			"计算编号 OOD-%s 的十二维辛矩阵全部特征值，并引用实验室原始谱仪校准表。",
			"查询编号 OOD-%s 的火星轨道器推进剂阀门扭矩和遥测校验和。",
			"给出编号 OOD-%s 的古代楔形文字泥板逐字转写及馆藏温湿度记录。",
			"说明编号 OOD-%s 的海底中微子阵列第七码元件电压和维护批次。",
		}
		base.Query = fmt.Sprintf(questions[ordinal%len(questions)], code)
		base.ExpectedAnswer = "问题超出冻结图文笔记语料范围，且该编号不存在；应返回 no_relevant_document，不得用主题相近但无关的笔记拼接答案。"
		base.AdversarialTags = []string{"out_of_domain_noise", "domain_shift", "false_positive_trap"}
		base.Metadata["expected_answerability"] = "no_relevant_document"
		add(target)
		add(peersFor(index, target, 2)...)
	default:
		return AuthoredCase{}, fmt.Errorf("unsupported task %q", slot.TaskType)
	}

	for offset := 1; len(pool) < 6; offset++ {
		add(index.publicNotes[(stableIndex(slot.CaseID, len(index.publicNotes))+offset*193)%len(index.publicNotes)])
	}
	base.CandidateRefs = shuffledRefs(slot.CaseID, pool[:min(6, len(pool))])
	base.Metadata["candidate_pool_policy"] = "target_plus_near_duplicate_and_cross_topic_distractors"
	return base, nil
}

type sourceMetrics struct{ Days, Records, Budget int }

func metricsOf(source DraftSource) sourceMetrics {
	return sourceMetrics{Days: firstInt(daysPattern, source.Body), Records: firstInt(recordsPattern, source.Body), Budget: firstInt(budgetPattern, source.Body)}
}

func firstInt(pattern *regexp.Regexp, value string) int {
	match := pattern.FindStringSubmatch(value)
	if len(match) != 2 {
		return 0
	}
	var result int
	_, _ = fmt.Sscanf(match[1], "%d", &result)
	return result
}

func subjectOf(source DraftSource) string {
	if len(source.Tags) > 1 && strings.TrimSpace(source.Tags[1]) != "" {
		return strings.TrimSpace(source.Tags[1])
	}
	if len(source.Topics) > 0 {
		return strings.TrimSpace(source.Topics[0])
	}
	return strings.TrimSpace(source.Category)
}

func audienceOf(source DraftSource) string {
	start := strings.Index(source.Body, "对")
	end := strings.Index(source.Body, "而言")
	if start >= 0 && end > start && end-start < 120 {
		return strings.TrimSpace(source.Body[start+len("对") : end])
	}
	return "原文描述的目标读者"
}

func conclusionOf(source DraftSource) string {
	start := strings.Index(source.Body, "【先说结论】")
	end := strings.Index(source.Body, "【具体过程】")
	if start >= 0 && end > start {
		return strings.TrimSpace(source.Body[start+len("【先说结论】") : end])
	}
	return strings.TrimSpace(source.Canonical)
}

func recordNumber(source DraftSource) int64 {
	match := recordPattern.FindStringSubmatch(source.Title)
	if len(match) == 2 {
		var result int64
		if _, err := fmt.Sscanf(match[1], "%d", &result); err == nil && result > 0 {
			return result
		}
	}
	return source.NoteID
}

func typoVariantDraft(value string) string {
	runes := []rune(strings.TrimSpace(value))
	if len(runes) < 3 {
		return value + "方按"
	}
	index := len(runes) / 2
	return string(append(runes[:index], runes[index+1:]...))
}

func pickNote(notes []DraftSource, caseID string, ordinal int) DraftSource {
	return notes[(stableIndex(caseID, len(notes))+ordinal*149)%len(notes)]
}

func peersFor(index draftIndex, target DraftSource, count int) []DraftSource {
	peers := index.notesBySubject[subjectOf(target)]
	result := make([]DraftSource, 0, count)
	for _, peer := range peers {
		if peer.SourceID == target.SourceID {
			continue
		}
		result = append(result, peer)
		if len(result) == count {
			break
		}
	}
	return result
}

func mediaFor(index draftIndex, noteID int64, excludedSourceID int64) []DraftSource {
	result := []DraftSource{}
	for _, media := range index.mediaByNote[noteID] {
		if media.SourceID != excludedSourceID {
			result = append(result, media)
		}
		if len(result) == 1 {
			break
		}
	}
	return result
}

func temporalPair(index draftIndex, target DraftSource, ordinal int) [2]DraftSource {
	peers := index.notesBySubject[subjectOf(target)]
	if len(peers) < 2 {
		peer := differentSubject(index, target, ordinal)
		if peer.CreatedAt.Before(target.CreatedAt) {
			return [2]DraftSource{peer, target}
		}
		return [2]DraftSource{target, peer}
	}
	position := stableIndex(target.Title, len(peers)-1)
	left, right := peers[position], peers[position+1]
	return [2]DraftSource{left, right}
}

func differentSubject(index draftIndex, target DraftSource, ordinal int) DraftSource {
	start := (stableIndex(target.Title, len(index.publicNotes)) + ordinal*211) % len(index.publicNotes)
	for offset := 0; offset < len(index.publicNotes); offset++ {
		candidate := index.publicNotes[(start+offset)%len(index.publicNotes)]
		if subjectOf(candidate) != subjectOf(target) {
			return candidate
		}
	}
	return index.publicNotes[(start+1)%len(index.publicNotes)]
}

func appendUniqueSources(existing []DraftSource, additions ...DraftSource) []DraftSource {
	seen := make(map[string]struct{}, len(existing)+len(additions))
	for _, source := range existing {
		seen[refKey(source.CandidateRef)] = struct{}{}
	}
	for _, source := range additions {
		if source.SourceID <= 0 {
			continue
		}
		key := refKey(source.CandidateRef)
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		seen[key] = struct{}{}
		existing = append(existing, source)
	}
	return existing
}

func shuffledRefs(caseID string, sources []DraftSource) []CandidateRef {
	refs := make([]CandidateRef, 0, len(sources))
	for _, source := range sources {
		refs = append(refs, source.CandidateRef)
	}
	sort.Slice(refs, func(i, j int) bool {
		return stableValue(caseID+refKey(refs[i])) < stableValue(caseID+refKey(refs[j]))
	})
	return refs
}

func stableIndex(value string, size int) int {
	if size <= 0 {
		return 0
	}
	return int(stableValue(value) % uint64(size))
}

func stableValue(value string) uint64 {
	digest := sha256.Sum256([]byte(value))
	return binary.BigEndian.Uint64(digest[:8])
}

func shortCaseToken(value string) string {
	digest := sha256.Sum256([]byte(value))
	return fmt.Sprintf("%x", digest[:6])
}

func aclToken(canonical string) string {
	for _, field := range strings.FieldsFunc(canonical, func(r rune) bool {
		return !(r == '-' || r == '_' || r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z')
	}) {
		if strings.HasPrefix(field, "R7-") || strings.HasPrefix(field, "ACL-EVAL-R7-") {
			return strings.TrimPrefix(field, "ACL-EVAL-")
		}
	}
	if utf8.RuneCountInString(canonical) > 32 {
		return shortCaseToken(canonical)
	}
	return canonical
}
