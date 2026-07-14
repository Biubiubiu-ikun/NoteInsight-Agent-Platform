package contentgen

import (
	"encoding/json"
	"fmt"
	"math"
	"math/rand"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

var commentIntentPattern = []string{
	"positive_feedback", "ask_detail", "experience_share", "supplement",
	"ask_suitable", "positive_feedback", "disagreement", "experience_share",
	"ask_detail", "risk_warning", "positive_feedback", "request_followup",
	"supplement", "ask_suitable", "experience_share", "positive_feedback",
	"ask_detail", "disagreement", "supplement", "positive_feedback",
}

func Generate(cfg Config, userIDs []int64, creatorIDs []int64) (Corpus, Report, error) {
	cfg.Normalize()
	if err := cfg.Validate(); err != nil {
		return Corpus{}, Report{}, err
	}
	users := sortedIDs(userIDs)
	creators := sortedIDs(creatorIDs)
	if len(users) == 0 {
		return Corpus{}, Report{}, fmt.Errorf("corpus generation requires at least one active user")
	}
	if len(creators) == 0 {
		creators = users
	}

	corpus := Corpus{Config: cfg, Items: make([]Item, 0, cfg.Notes)}
	acc := newReportAccumulator(cfg)
	for noteIndex := 0; noteIndex < cfg.Notes; noteIndex++ {
		noteID := cfg.NoteIDStart + int64(noteIndex)
		category := categoryOrder[noteIndex%len(categoryOrder)]
		document, err := GenerateDocument(cfg.Seed, noteID, category, cfg.MediaPerNote, cfg.StartAt)
		if err != nil {
			return Corpus{}, Report{}, err
		}
		document.ProjectID = cfg.ProjectID
		document.AuthorID = creators[deterministicIndex(cfg.Seed, noteID, len(creators))]

		item := Item{
			Document: document,
			Comments: make([]Comment, 0, cfg.CommentsPerNote),
		}
		for commentIndex := 0; commentIndex < cfg.CommentsPerNote; commentIndex++ {
			commentID := cfg.CommentIDStart + int64(noteIndex*cfg.CommentsPerNote+commentIndex)
			comment := GenerateComment(cfg.Seed, document, commentID, commentIndex)
			comment.UserID = users[deterministicIndex(cfg.Seed+17, commentID, len(users))]
			item.Comments = append(item.Comments, comment)
		}
		item.EvalCases = GenerateEvalCases(document, cfg.EvalCasesPerNote)
		acc.record(item)
		corpus.Items = append(corpus.Items, item)
	}
	return corpus, acc.finish(cfg), nil
}

func GenerateDocument(seed int64, noteID int64, category string, mediaCount int, startAt time.Time) (Document, error) {
	candidates := themesForCategory(category)
	if len(candidates) == 0 {
		return Document{}, fmt.Errorf("unsupported content category %q", category)
	}
	if mediaCount < 1 || mediaCount > 9 {
		return Document{}, fmt.Errorf("media count must be between 1 and 9")
	}
	rng := rand.New(rand.NewSource(mixSeed(seed, noteID, int64(len(category)))))
	selected := candidates[rng.Intn(len(candidates))]
	days := 7 + rng.Intn(29)
	records := 4 + rng.Intn(13)
	budget := 80 + rng.Intn(13)*20
	variant := rng.Intn(4)
	titleTemplates := []string{
		"%d天实测：%s，结论和避坑都写清了",
		"不是种草清单：我的%s完整复盘，预算%d元",
		"%s怎么做更稳？%d次记录后的答案",
		"给%s的%s执行清单（%d天版）",
	}
	var title string
	switch variant {
	case 0:
		title = fmt.Sprintf(titleTemplates[variant], days, selected.subject)
	case 1:
		title = fmt.Sprintf(titleTemplates[variant], selected.subject, budget)
	case 2:
		title = fmt.Sprintf(titleTemplates[variant], selected.subject, records)
	default:
		title = fmt.Sprintf(titleTemplates[variant], selected.audience, selected.subject, days)
	}
	title = fmt.Sprintf("%s｜样本记录 %d", title, noteID)
	metric := fmt.Sprintf("%s；观察周期%d天，共完成%d次记录，相关预算约%d元", selected.metric, days, records, budget)
	conclusion := fmt.Sprintf("对%s而言，%s值得保留，但必须把“%s”当作前提，而不是忽略边界。", selected.audience, selected.positive[0], selected.concerns[0])
	scenario := Scenario{
		Subject:          selected.subject,
		Audience:         selected.audience,
		Context:          selected.context,
		Goal:             selected.goal,
		MainTopics:       []string{selected.steps[0], selected.steps[1], selected.concerns[0]},
		PositiveFeedback: append([]string(nil), selected.positive...),
		Concerns:         append([]string(nil), selected.concerns...),
		Steps:            append([]string(nil), selected.steps...),
		KeyMetric:        metric,
		Conclusion:       conclusion,
		NotSuitableFor:   selected.notSuitableFor,
	}
	body := fmt.Sprintf(`这不是一篇只给结论的清单。我在%s的条件下，围绕“%s”做了%d天记录，目标是%s。所有结果只代表这次合成样本设定，重点是把过程、限制和可复核数据写完整。

【先说结论】
%s

【具体过程】
1. %s。我先固定变量，避免第一天就同时更换太多条件。
2. %s。执行时保留失败记录，没有把不理想的结果删掉。
3. %s。最后比较投入、效果和长期维护成本。

【记录到的变化】
正向变化主要有：%s、%s、%s。%s。第%d次记录后结果开始稳定，但不同时间和环境仍有波动。

【争议和限制】
最需要警惕的是%s；另外，%s。它们也是评论区最值得继续核对的两个问题。我的做法不是唯一答案，更不能把个人体验当成普遍结论。

【适合谁】
更适合%s。%s不建议直接照搬，最好先补充自己的约束条件或寻求专业帮助。

【最终建议】
先用最小成本复现第一步，连续记录一周，再决定是否扩大投入。%s`,
		selected.context,
		selected.subject,
		days,
		selected.goal,
		conclusion,
		selected.steps[0],
		selected.steps[1],
		selected.steps[2],
		selected.positive[0],
		selected.positive[1],
		selected.positive[2],
		metric,
		records,
		selected.concerns[0],
		selected.concerns[1],
		selected.audience,
		selected.notSuitableFor,
		conclusion,
	)
	createdAt := startAt.Add(time.Duration(noteID%(120*24*60)) * time.Minute)
	document := Document{
		ID:       noteID,
		Title:    title,
		Body:     body,
		Category: category,
		Topics:   append([]string(nil), scenario.MainTopics...),
		Tags: []string{
			category,
			selected.subject,
			"实测复盘",
			"可检索语料",
		},
		Location: map[string]any{
			"city":      selected.location,
			"synthetic": true,
		},
		ProductEntities: []map[string]any{{
			"name":      selected.product,
			"type":      category,
			"synthetic": true,
		}},
		QualityScore: 0.88,
		CreatedAt:    createdAt,
		Scenario:     scenario,
	}
	document.Media = generateMedia(document, mediaCount, days, records, budget)
	return document, nil
}

func generateMedia(document Document, mediaCount int, days int, records int, budget int) []Media {
	templates := []struct {
		caption string
		ocr     string
	}{
		{
			caption: "封面结论卡：说明测试对象、适用人群和核心结论",
			ocr:     fmt.Sprintf("主题：%s\n适用人群：%s\n核心结论：%s", document.Scenario.Subject, document.Scenario.Audience, document.Scenario.Conclusion),
		},
		{
			caption: "执行步骤卡：把正文中的方法压缩成可操作清单",
			ocr:     fmt.Sprintf("执行清单\n01 %s\n02 %s\n03 %s", document.Scenario.Steps[0], document.Scenario.Steps[1], document.Scenario.Steps[2]),
		},
		{
			caption: "数据记录卡：展示观察周期、记录次数和预算边界",
			ocr:     fmt.Sprintf("记录卡\n观察周期：%d天\n有效记录：%d次\n预算：约%d元\n指标：%s", days, records, budget, document.Scenario.KeyMetric),
		},
		{
			caption: "避坑提醒卡：集中说明争议点和不适用人群",
			ocr:     fmt.Sprintf("避坑提醒\n风险一：%s\n风险二：%s\n不建议直接照搬：%s", document.Scenario.Concerns[0], document.Scenario.Concerns[1], document.Scenario.NotSuitableFor),
		},
	}
	media := make([]Media, 0, mediaCount)
	for index := 0; index < mediaCount; index++ {
		selected := templates[index%len(templates)]
		media = append(media, Media{
			Position: index + 1,
			Caption:  selected.caption,
			OCRText:  selected.ocr,
			Metadata: map[string]any{
				"synthetic":          true,
				"visual_placeholder": true,
				"content_role":       []string{"summary", "procedure", "measurement", "caveat"}[index%4],
			},
		})
	}
	return media
}

func GenerateComment(seed int64, document Document, commentID int64, index int) Comment {
	rng := rand.New(rand.NewSource(mixSeed(seed+97, document.ID, commentID)))
	intent := commentIntentPattern[index%len(commentIntentPattern)]
	topic := document.Scenario.MainTopics[(index/3)%len(document.Scenario.MainTopics)]
	positive := document.Scenario.PositiveFeedback[index%len(document.Scenario.PositiveFeedback)]
	concern := document.Scenario.Concerns[index%len(document.Scenario.Concerns)]
	days := 3 + (index*7)%31
	sequence := index + 1
	var content string
	var sentiment string
	switch intent {
	case "positive_feedback":
		content = fmt.Sprintf("看完《%s》很有共鸣。我也连续记录了%d天，%s确实比只看一次体验可靠；我的第%d次复盘里，%s仍然是最明显的变化。", document.Title, days, topic, sequence, positive)
		sentiment = "positive"
	case "ask_detail":
		content = fmt.Sprintf("关于《%s》想追问一个细节：执行“%s”时，如果第%d天出现%s，你会先降低频率还是直接停止？希望补充判断标准，这是我整理的第%d个问题。", document.Title, topic, days, concern, sequence)
		sentiment = "neutral"
	case "experience_share":
		content = fmt.Sprintf("补充第%d份不同样本。我按《%s》的思路试到第%d天，%s和你接近，但%s比预期更明显，所以我把第%d步拆成了两次完成。", sequence, document.Title, days, positive, concern, sequence%3+1)
		sentiment = "positive"
	case "supplement":
		content = fmt.Sprintf("给《%s》补一条可复核信息：除了%s，还可以同时记录环境、时间和投入。我的第%d条记录就是因为漏了这些变量，前后结果无法直接比较。", document.Title, topic, sequence)
		sentiment = "neutral"
	case "ask_suitable":
		content = fmt.Sprintf("《%s》提到更适合%s。如果是%s，并且只能坚持%d天，应该优先保留哪一步？这是评论区第%d条适用性提问。", document.Title, document.Scenario.Audience, document.Scenario.NotSuitableFor, days, sequence)
		sentiment = "neutral"
	case "disagreement":
		content = fmt.Sprintf("对《%s》里的“%s”我持保留意见。我的第%d次尝试中，%s带来的影响更大，可能需要把结论限定在%s这个场景。", document.Title, positive, sequence, concern, document.Scenario.Context)
		sentiment = "negative"
	case "risk_warning":
		content = fmt.Sprintf("第%d条风险提醒：后来看到《%s》的人不要跳过边界条件。尤其是%s，我在第%d天就遇到过类似问题；%s不适合直接照搬。", sequence, document.Title, concern, days, document.Scenario.NotSuitableFor)
		sentiment = "negative"
	case "request_followup":
		content = fmt.Sprintf("第%d条后续请求：希望《%s》能更新长期结果，特别是%s。现在%d天的记录已经很清楚，再补一个月后的变化会更有参考价值。", sequence, document.Title, topic, days)
		sentiment = "positive"
	default:
		content = fmt.Sprintf("《%s》的第%d条讨论：%s。", document.Title, sequence, topic)
		sentiment = "neutral"
	}
	return Comment{
		ID:        commentID,
		NoteID:    document.ID,
		Content:   content,
		Sentiment: sentiment,
		Intent:    intent,
		TopicID:   int64((index/3)%len(document.Scenario.MainTopics) + 1),
		CreatedAt: document.CreatedAt.Add(time.Duration(10+rng.Intn(7*24*60)) * time.Minute),
	}
}

func GenerateEvalCases(document Document, limit int) []EvalCase {
	cases := []EvalCase{
		{
			NoteID:         document.ID,
			TaskType:       "summary",
			Question:       fmt.Sprintf("《%s》的核心结论是什么？", document.Title),
			ExpectedAnswer: document.Scenario.Conclusion,
			GoldSources:    []GoldSource{{SourceType: "note_body", Topic: document.Scenario.Subject}, {SourceType: "media_ocr", Position: 1}},
		},
		{
			NoteID:   document.ID,
			TaskType: "procedure",
			Question: fmt.Sprintf("作者为%s采用了哪三个步骤？", document.Scenario.Goal),
			ExpectedAnswer: fmt.Sprintf("三个步骤依次是：%s；%s；%s。",
				document.Scenario.Steps[0], document.Scenario.Steps[1], document.Scenario.Steps[2]),
			GoldSources: []GoldSource{{SourceType: "note_body", Topic: "具体过程"}, {SourceType: "media_ocr", Position: 2}},
		},
		{
			NoteID:         document.ID,
			TaskType:       "controversy",
			Question:       fmt.Sprintf("围绕%s最值得关注的争议和风险是什么？", document.Scenario.Subject),
			ExpectedAnswer: fmt.Sprintf("主要风险是%s；另一个限制是%s。", document.Scenario.Concerns[0], document.Scenario.Concerns[1]),
			GoldSources:    []GoldSource{{SourceType: "note_body", Topic: document.Scenario.Concerns[0]}, {SourceType: "comment_cluster", Topic: document.Scenario.Concerns[0]}, {SourceType: "media_ocr", Position: 4}},
		},
		{
			NoteID:         document.ID,
			TaskType:       "audience",
			Question:       fmt.Sprintf("%s更适合谁，哪些人不应直接照搬？", document.Scenario.Subject),
			ExpectedAnswer: fmt.Sprintf("更适合%s；%s不应直接照搬。", document.Scenario.Audience, document.Scenario.NotSuitableFor),
			GoldSources:    []GoldSource{{SourceType: "note_body", Topic: "适合谁"}, {SourceType: "media_ocr", Position: 4}},
		},
		{
			NoteID:         document.ID,
			TaskType:       "ocr_detail",
			Question:       "图片记录卡里有哪些关键数据？",
			ExpectedAnswer: document.Scenario.KeyMetric,
			GoldSources:    []GoldSource{{SourceType: "media_ocr", Position: 3}},
		},
	}
	for index := range cases {
		cases[index].Metadata = map[string]any{
			"synthetic":        true,
			"scenario_subject": document.Scenario.Subject,
			"schema_version":   "phase5b_v1",
		}
	}
	return append([]EvalCase(nil), cases[:limit]...)
}

type reportAccumulator struct {
	report          Report
	titles          map[string]struct{}
	comments        map[string]int
	bodyCharacters  int64
	semanticAligned int64
}

func newReportAccumulator(cfg Config) *reportAccumulator {
	return &reportAccumulator{
		report: Report{
			RunID:                    cfg.RunID,
			Profile:                  cfg.Profile,
			Seed:                     cfg.Seed,
			MinimumBodyCharacters:    math.MaxInt,
			MinimumOCRCharacters:     math.MaxInt,
			MinimumCommentCharacters: math.MaxInt,
			CategoryCounts:           map[string]int64{},
			IntentCounts:             map[string]int64{},
			SentimentCounts:          map[string]int64{},
			EvalTaskCounts:           map[string]int64{},
		},
		titles:   map[string]struct{}{},
		comments: map[string]int{},
	}
}

func (a *reportAccumulator) record(item Item) {
	a.report.Notes++
	a.report.CategoryCounts[item.Document.Category]++
	a.titles[item.Document.Title] = struct{}{}
	bodyLength := utf8.RuneCountInString(item.Document.Body)
	a.bodyCharacters += int64(bodyLength)
	if bodyLength < a.report.MinimumBodyCharacters {
		a.report.MinimumBodyCharacters = bodyLength
	}
	for _, media := range item.Document.Media {
		a.report.Media++
		length := utf8.RuneCountInString(media.OCRText)
		if length < a.report.MinimumOCRCharacters {
			a.report.MinimumOCRCharacters = length
		}
	}
	for _, comment := range item.Comments {
		a.report.Comments++
		a.comments[comment.Content]++
		a.report.IntentCounts[comment.Intent]++
		a.report.SentimentCounts[comment.Sentiment]++
		length := utf8.RuneCountInString(comment.Content)
		if length < a.report.MinimumCommentCharacters {
			a.report.MinimumCommentCharacters = length
		}
		if semanticallyAligned(comment.Content, item.Document.Scenario) {
			a.semanticAligned++
		}
	}
	for _, evalCase := range item.EvalCases {
		a.report.EvalCases++
		a.report.EvalTaskCounts[evalCase.TaskType]++
	}
}

func (a *reportAccumulator) finish(cfg Config) Report {
	duplicateComments := 0
	for _, count := range a.comments {
		if count > 1 {
			duplicateComments += count - 1
		}
	}
	a.report.UniqueTitleRatio = roundedRatio(len(a.titles), a.report.Notes)
	a.report.DuplicateCommentRatio = roundedRatio(duplicateComments, a.report.Comments)
	a.report.SemanticAlignmentRatio = roundedRatio(int(a.semanticAligned), a.report.Comments)
	a.report.AverageBodyCharacters = rounded(float64(a.bodyCharacters) / float64(a.report.Notes))
	expectedCategories := min(len(categoryOrder), cfg.Notes)
	expectedEvalTypes := min(5, cfg.EvalCasesPerNote)
	a.report.Checks = []QualityCheck{
		{Name: "category_diversity", Value: float64(len(a.report.CategoryCounts)), Target: fmt.Sprintf(">= %d", expectedCategories), Passed: len(a.report.CategoryCounts) >= expectedCategories},
		{Name: "unique_titles", Value: a.report.UniqueTitleRatio, Target: ">= 0.98", Passed: a.report.UniqueTitleRatio >= 0.98},
		{Name: "substantive_note_body", Value: float64(a.report.MinimumBodyCharacters), Target: ">= 300 characters", Passed: a.report.MinimumBodyCharacters >= 300},
		{Name: "substantive_media_ocr", Value: float64(a.report.MinimumOCRCharacters), Target: ">= 30 characters", Passed: a.report.MinimumOCRCharacters >= 30},
		{Name: "substantive_comments", Value: float64(a.report.MinimumCommentCharacters), Target: ">= 35 characters", Passed: a.report.MinimumCommentCharacters >= 35},
		{Name: "low_comment_duplication", Value: a.report.DuplicateCommentRatio, Target: "<= 0.01", Passed: a.report.DuplicateCommentRatio <= 0.01},
		{Name: "semantic_alignment", Value: a.report.SemanticAlignmentRatio, Target: ">= 0.98", Passed: a.report.SemanticAlignmentRatio >= 0.98},
		{Name: "eval_task_diversity", Value: float64(len(a.report.EvalTaskCounts)), Target: fmt.Sprintf(">= %d", expectedEvalTypes), Passed: len(a.report.EvalTaskCounts) >= expectedEvalTypes},
	}
	return a.report
}

func semanticallyAligned(content string, scenario Scenario) bool {
	if strings.Contains(content, scenario.Subject) {
		return true
	}
	groups := [][]string{scenario.MainTopics, scenario.PositiveFeedback, scenario.Concerns}
	for _, group := range groups {
		for _, value := range group {
			if strings.Contains(content, value) {
				return true
			}
		}
	}
	return false
}

func sortedIDs(values []int64) []int64 {
	result := append([]int64(nil), values...)
	sort.Slice(result, func(i int, j int) bool { return result[i] < result[j] })
	return result
}

func deterministicIndex(seed int64, id int64, length int) int {
	rng := rand.New(rand.NewSource(mixSeed(seed, id, int64(length))))
	return rng.Intn(length)
}

func mixSeed(seed int64, values ...int64) int64 {
	result := uint64(seed) ^ 0x9e3779b97f4a7c15
	for _, value := range values {
		result ^= uint64(value) + 0x9e3779b97f4a7c15 + (result << 6) + (result >> 2)
	}
	return int64(result)
}

func roundedRatio(numerator int, denominator int) float64 {
	if denominator == 0 {
		return 0
	}
	return rounded(float64(numerator) / float64(denominator))
}

func rounded(value float64) float64 {
	return math.Round(value*10_000) / 10_000
}

func JSON(value any) (string, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}
