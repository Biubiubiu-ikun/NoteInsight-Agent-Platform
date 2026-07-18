package retrieval

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"creatorinsight/backend-go/internal/evidence"
)

var (
	noteIDPattern     = regexp.MustCompile(`(?:笔记|样本记录|记录)\s*([0-9]{1,12})`)
	longNumberPattern = regexp.MustCompile(`\b([1-9][0-9]{3,11})\b`)
	positionPattern   = regexp.MustCompile(`位置\s*([0-9]{1,6})`)
	subjectPattern    = regexp.MustCompile(`(?:根据|查找)[“"]([^”"]{2,80})[”"]`)
)

var genericBigrams = map[string]struct{}{
	"一个": {}, "一份": {}, "不能": {}, "不要": {}, "什么": {}, "以及": {}, "作者": {},
	"依据": {}, "其中": {}, "分别": {}, "只要": {}, "只能": {}, "可以": {}, "如何": {},
	"是否": {}, "最终": {}, "有关": {}, "根据": {}, "样本": {}, "每个": {}, "没有": {},
	"然后": {}, "现在": {}, "相关": {}, "笔记": {}, "给出": {}, "记录": {}, "这个": {},
	"这些": {}, "这份": {}, "那个": {}, "那些": {}, "里面": {}, "需要": {}, "问题": {},
}

func BuildQueryPlan(raw string) (QueryPlan, error) {
	query := strings.TrimSpace(raw)
	if query == "" {
		return QueryPlan{}, fmt.Errorf("%w: query is required", ErrInvalidInput)
	}
	if utf8.RuneCountInString(query) > MaxQueryRunes {
		return QueryPlan{}, fmt.Errorf("%w: query exceeds %d characters", ErrInvalidInput, MaxQueryRunes)
	}

	tokens := evidence.Tokenize(query)
	terms := make([]string, 0, min(len(tokens), MaxQueryTerms))
	singleHan := make([]string, 0)
	seen := make(map[string]struct{}, len(tokens))
	for _, token := range tokens {
		if _, found := seen[token]; found {
			continue
		}
		if isHanToken(token, 1) {
			singleHan = append(singleHan, token)
			continue
		}
		if isHanToken(token, 2) {
			if _, generic := genericBigrams[token]; generic {
				continue
			}
		} else if utf8.RuneCountInString(token) < 2 && !isNumeric(token) {
			continue
		}
		seen[token] = struct{}{}
		terms = append(terms, token)
		if len(terms) == MaxQueryTerms {
			break
		}
	}
	if len(terms) == 0 {
		for _, token := range singleHan {
			if _, found := seen[token]; found {
				continue
			}
			seen[token] = struct{}{}
			terms = append(terms, token)
			if len(terms) == MaxQueryTerms {
				break
			}
		}
	}

	plan := QueryPlan{
		Original:      query,
		Terms:         terms,
		HintedNoteIDs: extractInt64Matches(noteIDPattern, query),
		PreferredType: "note",
	}
	if match := subjectPattern.FindStringSubmatch(query); len(match) == 2 {
		plan.SubjectTerms = meaningfulTerms(match[1], MaxQueryTerms)
	}
	if strings.Contains(query, "笔记") {
		plan.HintedNoteIDs = mergeInt64(plan.HintedNoteIDs, extractInt64Matches(longNumberPattern, query))
	}
	lower := strings.ToLower(query)
	switch {
	case strings.Contains(lower, "ocr") || strings.Contains(query, "图片文字") || strings.Contains(query, "图中文字"):
		plan.PreferredType = "note_media"
	case strings.Contains(query, "评论区") || strings.Contains(query, "评论内容"):
		plan.PreferredType = "note_comment_cluster"
	case strings.Contains(query, "日事实") || strings.Contains(query, "每日事实"):
		plan.PreferredType = "note_daily_fact"
	}
	if matches := positionPattern.FindStringSubmatch(query); len(matches) == 2 {
		if position, err := strconv.Atoi(matches[1]); err == nil {
			plan.PreferredPosition = &position
		}
	}
	return plan, nil
}

func meaningfulTerms(value string, limit int) []string {
	tokens := evidence.Tokenize(value)
	terms := make([]string, 0, min(len(tokens), limit))
	seen := make(map[string]struct{}, len(tokens))
	for _, token := range tokens {
		if _, found := seen[token]; found || isHanToken(token, 1) {
			continue
		}
		if isHanToken(token, 2) {
			if _, generic := genericBigrams[token]; generic {
				continue
			}
		} else if utf8.RuneCountInString(token) < 2 && !isNumeric(token) {
			continue
		}
		seen[token] = struct{}{}
		terms = append(terms, token)
		if len(terms) == limit {
			break
		}
	}
	return terms
}

func BuildTSQuery(terms []string) string {
	if len(terms) == 0 {
		return ""
	}
	if len(terms) == 1 {
		return terms[0]
	}
	if len(terms) == 2 {
		return terms[0] + " & " + terms[1]
	}
	terms = terms[:min(len(terms), 6)]
	pairs := make([]string, 0, len(terms)*(len(terms)-1)/2)
	for left := 0; left < len(terms); left++ {
		for right := left + 1; right < len(terms); right++ {
			pairs = append(pairs, "("+terms[left]+" & "+terms[right]+")")
		}
	}
	return strings.Join(pairs, " | ")
}

func isHanToken(token string, wanted int) bool {
	if utf8.RuneCountInString(token) != wanted {
		return false
	}
	for _, current := range token {
		if !unicode.Is(unicode.Han, current) {
			return false
		}
	}
	return true
}

func isNumeric(token string) bool {
	if token == "" {
		return false
	}
	for _, current := range token {
		if !unicode.IsDigit(current) {
			return false
		}
	}
	return true
}

func extractInt64Matches(pattern *regexp.Regexp, value string) []int64 {
	matches := pattern.FindAllStringSubmatch(value, -1)
	result := make([]int64, 0, len(matches))
	seen := make(map[int64]struct{}, len(matches))
	for _, match := range matches {
		parsed, err := strconv.ParseInt(match[1], 10, 64)
		if err != nil || parsed <= 0 {
			continue
		}
		if _, found := seen[parsed]; found {
			continue
		}
		seen[parsed] = struct{}{}
		result = append(result, parsed)
	}
	return result
}

func mergeInt64(left []int64, right []int64) []int64 {
	seen := make(map[int64]struct{}, len(left)+len(right))
	result := make([]int64, 0, len(left)+len(right))
	for _, values := range [][]int64{left, right} {
		for _, value := range values {
			if _, found := seen[value]; found {
				continue
			}
			seen[value] = struct{}{}
			result = append(result, value)
		}
	}
	return result
}
