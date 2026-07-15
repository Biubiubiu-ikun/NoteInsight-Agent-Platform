package contentgen

import (
	"reflect"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestGenerateIsDeterministicAndPassesQualityChecks(t *testing.T) {
	cfg, err := PresetFor("smoke", 20260714)
	if err != nil {
		t.Fatalf("PresetFor() error = %v", err)
	}
	cfg.NoteIDStart = 1000
	cfg.CommentIDStart = 10000
	users := sequentialIDs(1, 100)
	creators := sequentialIDs(1, 10)

	firstCorpus, firstReport, err := Generate(cfg, users, creators)
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	secondCorpus, secondReport, err := Generate(cfg, users, creators)
	if err != nil {
		t.Fatalf("Generate() second error = %v", err)
	}
	if !reflect.DeepEqual(firstCorpus, secondCorpus) || !reflect.DeepEqual(firstReport, secondReport) {
		t.Fatal("fixed seed corpus is not deterministic")
	}
	if firstReport.Notes != 20 || firstReport.Media != 60 || firstReport.Comments != 600 || firstReport.EvalCases != 161 {
		t.Fatalf("unexpected report counts: %+v", firstReport)
	}
	for _, check := range firstReport.Checks {
		if !check.Passed {
			t.Errorf("quality check %s failed: value=%v target=%s", check.Name, check.Value, check.Target)
		}
	}
}

func TestGeneratedTextIsSubstantiveAndSemanticallyLinked(t *testing.T) {
	cfg, err := PresetFor("smoke", 99)
	if err != nil {
		t.Fatalf("PresetFor() error = %v", err)
	}
	cfg.Notes = 10
	cfg.CommentsPerNote = 20
	cfg.NoteIDStart = 2000
	cfg.CommentIDStart = 20000
	corpus, _, err := Generate(cfg, sequentialIDs(1, 30), sequentialIDs(1, 5))
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}

	for _, item := range corpus.Items {
		document := item.Document
		if utf8.RuneCountInString(document.Body) < 300 {
			t.Errorf("note %d body is too short", document.ID)
		}
		if !strings.Contains(document.Body, document.Scenario.Concerns[0]) || !strings.Contains(document.Body, document.Scenario.Steps[0]) {
			t.Errorf("note %d body does not encode hidden scenario", document.ID)
		}
		for _, media := range document.Media {
			if media.Caption == "" || utf8.RuneCountInString(media.OCRText) < 30 {
				t.Errorf("note %d has weak media text: %+v", document.ID, media)
			}
		}
		for _, comment := range item.Comments {
			if !strings.Contains(comment.Content, document.Title) {
				t.Errorf("comment %d is not linked to its note", comment.ID)
			}
			if comment.Intent == "" || comment.Sentiment == "" || comment.TopicID == 0 {
				t.Errorf("comment %d lacks semantic labels", comment.ID)
			}
		}
		for _, evalCase := range item.EvalCases {
			answerable, _ := evalCase.Metadata["answerable"].(bool)
			if evalCase.Question == "" || evalCase.ExpectedAnswer == "" || (answerable && len(evalCase.GoldSources) == 0) {
				t.Errorf("note %d has incomplete eval case: %+v", document.ID, evalCase)
			}
		}
	}
}

func TestQualityProfilePassesAllChecks(t *testing.T) {
	cfg, err := PresetFor("quality", 20260714)
	if err != nil {
		t.Fatalf("PresetFor() error = %v", err)
	}
	cfg.NoteIDStart = 100000
	cfg.CommentIDStart = 1000000
	_, report, err := Generate(cfg, sequentialIDs(1, 1000), sequentialIDs(1, 100))
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	for _, check := range report.Checks {
		if !check.Passed {
			t.Errorf("quality check %s failed: value=%v target=%s", check.Name, check.Value, check.Target)
		}
	}
	if report.Comments != 40000 || report.EvalCases != 1619 {
		t.Fatalf("unexpected quality profile volume: %+v", report)
	}
}

func TestGenerateRejectsMissingUsers(t *testing.T) {
	cfg, err := PresetFor("smoke", 1)
	if err != nil {
		t.Fatalf("PresetFor() error = %v", err)
	}
	cfg.NoteIDStart = 1
	cfg.CommentIDStart = 1
	if _, _, err := Generate(cfg, nil, nil); err == nil {
		t.Fatal("Generate() expected missing-user error")
	}
}

func TestGenerateDocumentRejectsUnsupportedCategory(t *testing.T) {
	if _, err := GenerateDocument(1, 1, "unknown", 3, mustPresetStart(t)); err == nil {
		t.Fatal("GenerateDocument() expected unsupported-category error")
	}
}

func sequentialIDs(start int64, count int) []int64 {
	values := make([]int64, count)
	for index := range values {
		values[index] = start + int64(index)
	}
	return values
}

func mustPresetStart(t *testing.T) time.Time {
	t.Helper()
	cfg, err := PresetFor("smoke", 1)
	if err != nil {
		t.Fatalf("PresetFor() error = %v", err)
	}
	return cfg.StartAt
}
