package main

import (
	"math/rand"
	"testing"

	"creatorinsight/backend-go/internal/contentgen"
)

func TestBuiltInProfilesAreValid(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"dev", "capacity", "million-comments"} {
		profile, err := selectProfile(name)
		if err != nil {
			t.Fatalf("selectProfile(%q): %v", name, err)
		}
		if err := profile.validate(); err != nil {
			t.Fatalf("profile %q is invalid: %v", name, err)
		}
		if profile.estimatedRows() <= int64(profile.comments) {
			t.Fatalf("profile %q estimated rows do not include related data", name)
		}
	}
}

func TestProfileOverridesAndValidation(t *testing.T) {
	t.Parallel()

	profile := devProfile.withOverrides(profileConfig{users: 20, creators: 4, notes: 30, comments: 90})
	if profile.users != 20 || profile.creators != 4 || profile.notes != 30 || profile.comments != 90 {
		t.Fatalf("unexpected overrides: %+v", profile)
	}

	invalid := profile
	invalid.creators = invalid.users + 1
	if err := invalid.validate(); err == nil {
		t.Fatal("expected creators greater than users to fail validation")
	}
}

func TestCommentTiersCoverExactVolume(t *testing.T) {
	t.Parallel()

	tiers := buildCommentTiers(100, 1000)
	var notes int
	var comments int
	for _, tier := range tiers {
		if tier.noteCount <= 0 || tier.commentCount < 0 {
			t.Fatalf("invalid tier: %+v", tier)
		}
		notes += tier.noteCount
		comments += tier.commentCount
	}
	if notes != 100 || comments != 1000 {
		t.Fatalf("tier totals notes=%d comments=%d", notes, comments)
	}
	if tiers[0].noteCount != 5 || tiers[0].commentCount != 400 {
		t.Fatalf("hot tier does not preserve the intended long tail: %+v", tiers[0])
	}
}

func TestUniquePairSequenceIsDeterministicAndUnique(t *testing.T) {
	t.Parallel()

	collect := func(seed int64) [][2]int {
		sequence, err := newUniquePairSequence(rand.New(rand.NewSource(seed)), 200, 20, 20)
		if err != nil {
			t.Fatalf("newUniquePairSequence: %v", err)
		}
		pairs := make([][2]int, 0, 200)
		seen := make(map[[2]int]struct{}, 200)
		for {
			left, right, ok := sequence.next()
			if !ok {
				break
			}
			pair := [2]int{left, right}
			if _, exists := seen[pair]; exists {
				t.Fatalf("duplicate pair: %v", pair)
			}
			seen[pair] = struct{}{}
			pairs = append(pairs, pair)
		}
		return pairs
	}

	first := collect(42)
	second := collect(42)
	if len(first) != 200 || len(second) != 200 {
		t.Fatalf("unexpected pair counts: %d and %d", len(first), len(second))
	}
	for i := range first {
		if first[i] != second[i] {
			t.Fatalf("sequence differs at %d: %v != %v", i, first[i], second[i])
		}
	}
}

func TestCategoryForNoteIsStableAndCoversCatalog(t *testing.T) {
	t.Parallel()

	categories := contentgen.Categories()
	seen := make(map[string]struct{}, len(categories))
	for noteID := int64(1); noteID <= 1000; noteID++ {
		first := categoryForNote(20260714, noteID, categories)
		second := categoryForNote(20260714, noteID, categories)
		if first != second {
			t.Fatalf("category is not deterministic for note %d", noteID)
		}
		seen[first] = struct{}{}
	}
	if len(seen) != len(categories) {
		t.Fatalf("covered %d categories, want %d", len(seen), len(categories))
	}
}
