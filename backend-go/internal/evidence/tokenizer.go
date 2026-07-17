package evidence

import (
	"strings"
	"unicode"
)

func Tokenize(text string) []string {
	tokens := make([]string, 0)
	word := make([]rune, 0, 24)
	han := make([]rune, 0, 24)
	flushWord := func() {
		if len(word) > 0 {
			tokens = append(tokens, strings.ToLower(string(word)))
			word = word[:0]
		}
	}
	flushHan := func() {
		if len(han) == 0 {
			return
		}
		for _, current := range han {
			tokens = append(tokens, string(current))
		}
		for index := 0; index+1 < len(han); index++ {
			tokens = append(tokens, string(han[index:index+2]))
		}
		han = han[:0]
	}
	for _, current := range text {
		switch {
		case unicode.Is(unicode.Han, current):
			flushWord()
			han = append(han, current)
		case unicode.IsLetter(current) || unicode.IsDigit(current):
			flushHan()
			word = append(word, current)
		default:
			flushWord()
			flushHan()
		}
	}
	flushWord()
	flushHan()
	return tokens
}

func lexicalText(text string) string {
	return strings.Join(Tokenize(text), " ")
}
