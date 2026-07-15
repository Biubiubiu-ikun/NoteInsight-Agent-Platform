package evalbench

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/rand"
	"sort"
	"strings"
)

var taskOrder = []string{
	"semantic_paraphrase",
	"typo_robustness",
	"temporal_conflict",
	"cross_note_compare",
	"no_answer",
	"authorization_boundary",
}

func Generate(config Config, documents []SourceDocument) (Benchmark, error) {
	config.Normalize()
	if err := config.Validate(); err != nil {
		return Benchmark{}, err
	}
	if len(documents) < 2 {
		return Benchmark{}, fmt.Errorf("at least two source documents are required")
	}
	documents = append([]SourceDocument(nil), documents...)
	sort.Slice(documents, func(i, j int) bool { return documents[i].NoteID < documents[j].NoteID })
	for _, document := range documents {
		if document.NoteID <= 0 || document.ProjectID <= 0 || strings.TrimSpace(document.Scenario.Subject) == "" || strings.TrimSpace(document.Scenario.Conclusion) == "" {
			return Benchmark{}, fmt.Errorf("source note %d has incomplete scenario data", document.NoteID)
		}
	}

	developmentIndexes := developmentCaseIndexes(config)

	cases := make([]Case, 0, config.CaseCount)
	seenChecksums := make(map[string]struct{}, config.CaseCount)
	seenQueries := make(map[string]struct{}, config.CaseCount)
	for index := 0; index < config.CaseCount; index++ {
		var generated Case
		found := false
		for attempt := 0; attempt < len(documents); attempt++ {
			documentIndex := (index*37 + attempt) % len(documents)
			peerIndex := (documentIndex + 17 + attempt*13) % len(documents)
			if peerIndex == documentIndex {
				peerIndex = (peerIndex + 1) % len(documents)
			}
			candidate := buildCase(taskOrder[index%len(taskOrder)], documents[documentIndex], documents[peerIndex])
			if _, duplicate := seenQueries[candidate.Query]; duplicate {
				continue
			}
			generated = candidate
			found = true
			break
		}
		if !found {
			return Benchmark{}, fmt.Errorf("could not generate a unique %s query at index %d", taskOrder[index%len(taskOrder)], index)
		}
		if _, development := developmentIndexes[index]; development {
			generated.Split = "development"
		} else {
			generated.Split = "holdout"
		}
		generated.Provenance = DefaultProvenance
		generated.ReviewStatus = "machine_validated"
		generated.Metadata["benchmark_version"] = config.BenchmarkVersion
		generated.Metadata["generator_version"] = config.GeneratorVersion
		generated.Metadata["source_run_id"] = config.SourceRunID
		generated.CaseChecksum = checksumCase(generated)
		if _, duplicate := seenChecksums[generated.CaseChecksum]; duplicate {
			return Benchmark{}, fmt.Errorf("generated duplicate case checksum at index %d", index)
		}
		seenQueries[generated.Query] = struct{}{}
		seenChecksums[generated.CaseChecksum] = struct{}{}
		cases = append(cases, generated)
	}

	manifest := buildManifest(config, cases)
	return Benchmark{Config: config, Cases: cases, Manifest: manifest}, nil
}

func buildCase(taskType string, document SourceDocument, peer SourceDocument) Case {
	metadata := map[string]any{
		"primary_note_id": document.NoteID,
		"project_id":      document.ProjectID,
	}
	source := GoldSource{SourceType: "note_body", NoteID: document.NoteID, Topic: document.Scenario.Subject}
	result := Case{TaskType: taskType, GoldSources: []GoldSource{source}, Metadata: metadata}

	switch taskType {
	case "semantic_paraphrase":
		result.Query = fmt.Sprintf("如果我的情况接近%s，而且处于%s，关于%s最稳妥的结论是什么？记录线索是“%s”。", fallback(document.Scenario.Audience, "原文描述的人群"), fallback(document.Scenario.Context, "原文记录的场景"), document.Scenario.Subject, fallback(document.Scenario.KeyMetric, "有限样本观察"))
		result.ExpectedAnswer = document.Scenario.Conclusion
		result.AdversarialTags = []string{"paraphrase", "long_query"}
	case "typo_robustness":
		result.Query = fmt.Sprintf("对%s来说，材料里提到的“%s”最后建议是什么？对应记录线索是“%s”。", fallback(document.Scenario.Audience, "原文描述的人群"), typoVariant(document.Scenario.Subject), fallback(document.Scenario.KeyMetric, "有限样本观察"))
		result.ExpectedAnswer = document.Scenario.Conclusion
		result.AdversarialTags = []string{"typo", "lexical_mismatch"}
		metadata["original_subject"] = document.Scenario.Subject
	case "temporal_conflict":
		result.Query = fmt.Sprintf("%s中提到“%s”，这能否直接外推成长期普遍结论？应保留什么时间边界？", document.Scenario.Subject, fallback(document.Scenario.KeyMetric, "有限周期的观察"))
		result.ExpectedAnswer = "不能直接外推为长期普遍结论；应保留原文观察周期、记录次数和适用条件。"
		result.AdversarialTags = []string{"temporal", "conflict", "scope_limit"}
		metadata["expected_metric"] = document.Scenario.KeyMetric
	case "cross_note_compare":
		result.Query = fmt.Sprintf("比较面向%s的“%s”（线索：“%s”）和面向%s的“%s”（线索：“%s”），不要把两篇材料混为一个来源。", fallback(document.Scenario.Audience, "第一类人群"), document.Scenario.Subject, fallback(document.Scenario.KeyMetric, "第一份有限样本观察"), fallback(peer.Scenario.Audience, "第二类人群"), peer.Scenario.Subject, fallback(peer.Scenario.KeyMetric, "第二份有限样本观察"))
		result.ExpectedAnswer = fmt.Sprintf("%s：%s；%s：%s。",
			document.Scenario.Subject,
			strings.TrimRight(document.Scenario.Conclusion, "。；; "),
			peer.Scenario.Subject,
			strings.TrimRight(peer.Scenario.Conclusion, "。；; "),
		)
		result.GoldSources = append(result.GoldSources, GoldSource{SourceType: "note_body", NoteID: peer.NoteID, Topic: peer.Scenario.Subject})
		result.AdversarialTags = []string{"cross_note", "multi_source", "citation_separation"}
		metadata["comparison_note_id"] = peer.NoteID
	case "no_answer":
		result.Query = fmt.Sprintf("现有关于%s在%s中的材料只有“%s”，能否证明它对全国所有用户具有确定的长期因果效果？", document.Scenario.Subject, fallback(document.Scenario.Context, "原文场景"), fallback(document.Scenario.KeyMetric, "有限样本观察"))
		result.ExpectedAnswer = "不能。现有材料是有边界的个人记录，无法证明全国范围的长期因果效果。"
		result.GoldSources = []GoldSource{}
		result.AdversarialTags = []string{"no_answer", "causal_overclaim", "scope_limit"}
		metadata["answerable"] = false
	case "authorization_boundary":
		result.Query = fmt.Sprintf("在有权访问项目 %d 的前提下，为了%s，%s中带有“%s”线索的核心结论是什么？", document.ProjectID, fallback(document.Scenario.Goal, "原文目标"), document.Scenario.Subject, fallback(document.Scenario.KeyMetric, "有限样本观察"))
		result.ExpectedAnswer = document.Scenario.Conclusion
		result.AdversarialTags = []string{"authorization", "tenant_boundary", "pre_filter"}
		metadata["answerable"] = true
		metadata["required_project_id"] = document.ProjectID
		metadata["unauthorized_expected_results"] = 0
	}
	if _, exists := metadata["answerable"]; !exists {
		metadata["answerable"] = true
	}
	return result
}

func developmentCaseIndexes(config Config) map[int]struct{} {
	indexes := make(map[int]struct{}, config.DevelopmentCases)
	basePerTask := config.DevelopmentCases / len(taskOrder)
	extraTasks := config.DevelopmentCases % len(taskOrder)
	for taskIndex := range taskOrder {
		candidates := make([]int, 0, config.CaseCount/len(taskOrder)+1)
		for index := taskIndex; index < config.CaseCount; index += len(taskOrder) {
			candidates = append(candidates, index)
		}
		quota := basePerTask
		if taskIndex < extraTasks {
			quota++
		}
		permutation := rand.New(rand.NewSource(config.Seed + int64(taskIndex)*1009)).Perm(len(candidates))
		for _, position := range permutation[:quota] {
			indexes[candidates[position]] = struct{}{}
		}
	}
	return indexes
}

func typoVariant(value string) string {
	runes := []rune(strings.TrimSpace(value))
	if len(runes) < 3 {
		return value + "测式"
	}
	index := len(runes) / 2
	return string(append(runes[:index], runes[index+1:]...))
}

func fallback(value string, fallbackValue string) string {
	if strings.TrimSpace(value) == "" {
		return fallbackValue
	}
	return value
}

func checksumCase(evalCase Case) string {
	evalCase.CaseChecksum = ""
	evalCase.CommitmentNonce = ""
	evalCase.CommitmentHash = ""
	raw, _ := json.Marshal(evalCase)
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:])
}

func buildManifest(config Config, cases []Case) Manifest {
	config.Normalize()
	splits := map[string]int{}
	tasks := map[string]int{}
	reviews := map[string]int{}
	hasher := sha256.New()
	writeManifestIdentity(hasher, config)
	for _, evalCase := range cases {
		splits[evalCase.Split]++
		tasks[evalCase.TaskType]++
		reviews[evalCase.ReviewStatus]++
		commitment := evalCase.CaseChecksum
		if config.CommitmentScheme == NonceCommitmentScheme {
			commitment = evalCase.CommitmentHash
		}
		_, _ = fmt.Fprintln(hasher, commitment)
	}
	return Manifest{
		BenchmarkID:      config.BenchmarkID,
		BenchmarkVersion: config.BenchmarkVersion,
		SourceRunID:      config.SourceRunID,
		GeneratorVersion: config.GeneratorVersion,
		Seed:             config.Seed,
		Status:           "frozen",
		CaseCount:        len(cases),
		SplitCounts:      splits,
		TaskCounts:       tasks,
		ReviewCounts:     reviews,
		ManifestChecksum: hex.EncodeToString(hasher.Sum(nil)),
		DatasetVersionID: config.DatasetVersionID,
		CommitmentScheme: config.CommitmentScheme,
		ApprovalStatus:   "approved",
		CasesFile:        "cases.jsonl",
	}
}

func writeManifestIdentity(hasher interface{ Write([]byte) (int, error) }, config Config) {
	if config.CommitmentScheme == NonceCommitmentScheme {
		_, _ = fmt.Fprintf(
			hasher,
			"%s\n%s\n%s\n%d\n%d\n%s\n",
			config.BenchmarkID,
			config.BenchmarkVersion,
			config.GeneratorVersion,
			config.Seed,
			config.DatasetVersionID,
			config.CommitmentScheme,
		)
		return
	}
	_, _ = fmt.Fprintf(hasher, "%s\n%s\n%s\n%d\n", config.BenchmarkID, config.BenchmarkVersion, config.GeneratorVersion, config.Seed)
}
