package simulator

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"math/rand"
	"sort"
	"time"
)

type Engine struct{}

type weightedState struct {
	state  State
	weight float64
}

type sessionPlan struct {
	startAt time.Time
	burst   bool
}

type reportAccumulator struct {
	report         Report
	noteEvents     map[int64]int64
	userEvents     map[int64]int64
	sessionLengths []int
	burstEvents    int64
}

func NewEngine() *Engine {
	return &Engine{}
}

func (e *Engine) Generate(ctx context.Context, cfg Config, dataset Dataset, sink Sink) (Report, error) {
	cfg.Normalize()
	if err := cfg.Validate(); err != nil {
		return Report{}, err
	}
	if err := dataset.Validate(); err != nil {
		return Report{}, err
	}
	if sink == nil {
		return Report{}, fmt.Errorf("simulator sink is required")
	}

	rng := rand.New(rand.NewSource(cfg.Seed))
	profiles := generateProfiles(rng, dataset.UserIDs)
	if err := sink.WriteProfiles(ctx, profiles); err != nil {
		sink.Abort(ctx, err)
		return Report{}, err
	}

	profileByUser := make(map[int64]UserProfile, len(profiles))
	userWeights := make([]float64, len(profiles))
	for i, profile := range profiles {
		profileByUser[profile.UserID] = profile
		userWeights[i] = math.Pow(profile.ActivityLevel+0.05, 2.2)
	}
	noteOrder := rng.Perm(len(dataset.Notes))
	zipf := rand.NewZipf(rng, cfg.ZipfExponent, 1, uint64(len(dataset.Notes)-1))
	plans := buildSessionPlans(rng, cfg)
	acc := newReportAccumulator(cfg, dataset, profiles)

	for sessionIndex, plan := range plans {
		if sessionIndex%1000 == 0 {
			select {
			case <-ctx.Done():
				sink.Abort(ctx, ctx.Err())
				return Report{}, ctx.Err()
			default:
			}
		}

		profile := profiles[weightedIndex(rng, userWeights)]
		note := dataset.Notes[noteOrder[int(zipf.Uint64())]]
		sessionID := fmt.Sprintf("%s_s%09d", cfg.RunID, sessionIndex+1)
		state := StateFeedImpression
		previous := State("")
		occurredAt := plan.startAt
		eventsInSession := 0

		for sequence := 1; sequence <= cfg.MaxSteps; sequence++ {
			commentID := int64(0)
			if state == StateCommentLiked && len(note.CommentIDs) > 0 {
				commentID = note.CommentIDs[rng.Intn(len(note.CommentIDs))]
			}
			event, err := makeEvent(cfg, profile, note, sessionID, sequence, state, previous, commentID, occurredAt, plan.burst, rng)
			if err != nil {
				sink.Abort(ctx, err)
				return Report{}, err
			}
			if err := sink.WriteEvent(ctx, event); err != nil {
				sink.Abort(ctx, err)
				return Report{}, err
			}
			acc.record(event, previous, note.Category, plan.burst)
			eventsInSession++

			if state == StateSessionExited {
				break
			}
			previous = state
			if sequence == cfg.MaxSteps-1 {
				state = StateSessionExited
			} else {
				state = nextState(rng, state, profile, len(note.CommentIDs) > 0)
			}
			occurredAt = occurredAt.Add(sessionStepDuration(rng, state))
		}
		acc.sessionLengths = append(acc.sessionLengths, eventsInSession)
	}

	report := acc.finish(cfg, dataset)
	if err := sink.Complete(ctx, report); err != nil {
		sink.Abort(ctx, err)
		return Report{}, err
	}
	return report, nil
}

func generateProfiles(rng *rand.Rand, userIDs []int64) []UserProfile {
	personas := []struct {
		persona Persona
		weight  float64
	}{
		{PersonaLurker, 0.40},
		{PersonaCommenter, 0.18},
		{PersonaCollector, 0.14},
		{PersonaFan, 0.10},
		{PersonaCritic, 0.08},
		{PersonaKnowledgeUser, 0.06},
		{PersonaCreator, 0.03},
		{PersonaSpammer, 0.01},
	}
	weights := make([]float64, len(personas))
	for i := range personas {
		weights[i] = personas[i].weight
	}

	profiles := make([]UserProfile, 0, len(userIDs))
	for _, userID := range userIDs {
		persona := personas[weightedIndex(rng, weights)].persona
		profile := profileForPersona(rng, userID, persona)
		profiles = append(profiles, profile)
	}
	return profiles
}

func profileForPersona(rng *rand.Rand, userID int64, persona Persona) UserProfile {
	type traits struct {
		activity, positive, like, collect, comment, share float64
		length                                            string
	}
	values := map[Persona]traits{
		PersonaLurker:        {0.18, 0.62, 0.05, 0.03, 0.01, 0.01, "short"},
		PersonaCommenter:     {0.62, 0.58, 0.20, 0.08, 0.32, 0.05, "medium"},
		PersonaCollector:     {0.48, 0.70, 0.18, 0.38, 0.05, 0.03, "short"},
		PersonaFan:           {0.72, 0.90, 0.48, 0.25, 0.18, 0.15, "medium"},
		PersonaCritic:        {0.58, 0.20, 0.12, 0.04, 0.35, 0.12, "medium"},
		PersonaKnowledgeUser: {0.55, 0.72, 0.20, 0.16, 0.30, 0.08, "long"},
		PersonaCreator:       {0.78, 0.76, 0.30, 0.12, 0.28, 0.24, "long"},
		PersonaSpammer:       {0.95, 0.45, 0.10, 0.02, 0.72, 0.35, "short"},
	}
	base := values[persona]
	paretoActivity := 0.08 / math.Pow(math.Max(rng.Float64(), 0.0001), 1/2.4)
	activity := clamp(base.activity*0.72+paretoActivity, 0.02, 1)
	return UserProfile{
		UserID:                  userID,
		Persona:                 persona,
		ActivityLevel:           rounded(clamp(activity+jitter(rng, 0.05), 0, 1)),
		PositiveRatio:           rounded(clamp(base.positive+jitter(rng, 0.08), 0, 1)),
		CommentLengthPreference: base.length,
		LikeProbability:         rounded(clamp(base.like+jitter(rng, 0.04), 0, 1)),
		CollectProbability:      rounded(clamp(base.collect+jitter(rng, 0.03), 0, 1)),
		CommentProbability:      rounded(clamp(base.comment+jitter(rng, 0.04), 0, 1)),
		ShareProbability:        rounded(clamp(base.share+jitter(rng, 0.03), 0, 1)),
	}
}

func buildSessionPlans(rng *rand.Rand, cfg Config) []sessionPlan {
	plans := make([]sessionPlan, cfg.Sessions)
	exponential := make([]float64, cfg.Sessions)
	total := 0.0
	for i := range exponential {
		total += rng.ExpFloat64()
		exponential[i] = total
	}
	for i := range plans {
		fraction := exponential[i] / total
		burst := cfg.Scenario != ScenarioOrganic && rng.Float64() < cfg.BurstFraction
		if burst {
			center := 0.18
			switch cfg.Scenario {
			case ScenarioControversy:
				center = 0.58
			case ScenarioMixed:
				if rng.Float64() < 0.45 {
					center = 0.65
				}
			}
			fraction = clamp(center+jitter(rng, 0.045), 0, 1)
		}
		plans[i] = sessionPlan{startAt: cfg.StartAt.Add(time.Duration(float64(cfg.Duration) * fraction)), burst: burst}
	}
	sort.SliceStable(plans, func(i, j int) bool { return plans[i].startAt.Before(plans[j].startAt) })
	return plans
}

func nextState(rng *rand.Rand, current State, profile UserProfile, hasComments bool) State {
	var candidates []weightedState
	switch current {
	case StateFeedImpression:
		candidates = []weightedState{{StateNoteViewed, 0.92}, {StateSessionExited, 0.08}}
	case StateNoteViewed:
		candidates = []weightedState{
			{StateMediaViewed, 0.24}, {StateCommentsViewed, 0.28},
			{StateNoteLiked, 0.10 + profile.LikeProbability},
			{StateNoteCollected, 0.04 + profile.CollectProbability},
			{StateNoteShared, 0.02 + profile.ShareProbability},
			{StateCommentCreated, 0.03 + profile.CommentProbability},
			{StateSessionExited, 0.25},
		}
	case StateMediaViewed:
		candidates = []weightedState{
			{StateCommentsViewed, 0.34}, {StateNoteLiked, 0.08 + profile.LikeProbability},
			{StateNoteCollected, 0.05 + profile.CollectProbability},
			{StateNoteShared, 0.03 + profile.ShareProbability},
			{StateCommentCreated, 0.02 + profile.CommentProbability},
			{StateSessionExited, 0.30},
		}
	case StateCommentsViewed:
		candidates = []weightedState{
			{StateCommentLiked, boolWeight(hasComments, 0.18+profile.LikeProbability*0.4)},
			{StateCommentCreated, 0.06 + profile.CommentProbability},
			{StateNoteLiked, 0.05 + profile.LikeProbability*0.5},
			{StateNoteCollected, 0.03 + profile.CollectProbability*0.5},
			{StateNoteShared, 0.02 + profile.ShareProbability*0.5},
			{StateSessionExited, 0.38},
		}
	case StateNoteLiked, StateNoteCollected, StateNoteShared, StateCommentCreated, StateCommentLiked:
		candidates = []weightedState{
			{StateCommentsViewed, 0.28},
			{StateNoteLiked, 0.04 + profile.LikeProbability*0.25},
			{StateNoteCollected, 0.03 + profile.CollectProbability*0.25},
			{StateNoteShared, 0.02 + profile.ShareProbability*0.25},
			{StateCommentCreated, 0.02 + profile.CommentProbability*0.25},
			{StateSessionExited, 0.55},
		}
	default:
		return StateSessionExited
	}

	for i := range candidates {
		candidates[i].weight *= personaStateMultiplier(profile.Persona, candidates[i].state)
	}
	weights := make([]float64, len(candidates))
	for i := range candidates {
		weights[i] = candidates[i].weight
	}
	return candidates[weightedIndex(rng, weights)].state
}

func personaStateMultiplier(persona Persona, state State) float64 {
	action := state == StateNoteLiked || state == StateNoteCollected || state == StateNoteShared || state == StateCommentCreated || state == StateCommentLiked
	switch persona {
	case PersonaLurker:
		if action {
			return 0.18
		}
	case PersonaCommenter:
		if state == StateCommentCreated || state == StateCommentLiked || state == StateCommentsViewed {
			return 2.2
		}
	case PersonaCollector:
		if state == StateNoteCollected {
			return 3.2
		}
	case PersonaFan:
		if action {
			return 1.8
		}
	case PersonaCritic, PersonaKnowledgeUser:
		if state == StateCommentCreated || state == StateCommentsViewed {
			return 2.1
		}
	case PersonaCreator:
		if state == StateNoteShared || state == StateCommentCreated {
			return 2.0
		}
	case PersonaSpammer:
		if state == StateCommentCreated || state == StateNoteShared {
			return 4.5
		}
	}
	return 1
}

func makeEvent(cfg Config, profile UserProfile, note NoteRef, sessionID string, sequence int, state State, previous State, commentID int64, occurredAt time.Time, burst bool, rng *rand.Rand) (Event, error) {
	payload := map[string]any{
		"category":       note.Category,
		"is_burst":       burst,
		"persona":        profile.Persona,
		"previous_state": previous,
		"scenario":       cfg.Scenario,
		"sequence_no":    sequence,
		"session_id":     sessionID,
		"state":          state,
	}
	if state == StateCommentCreated {
		payload["sentiment"] = sentiment(rng, profile.PositiveRatio)
		payload["intent"] = commentIntent(rng, profile.Persona)
		payload["length_preference"] = profile.CommentLengthPreference
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return Event{}, fmt.Errorf("marshal simulator event payload: %w", err)
	}
	return Event{
		SourceEventID: fmt.Sprintf("sim_%s_%09d_%02d", cfg.RunID, parseSessionNumber(sessionID), sequence),
		RunID:         cfg.RunID,
		SessionID:     sessionID,
		SequenceNo:    sequence,
		UserID:        profile.UserID,
		NoteID:        note.ID,
		CommentID:     commentID,
		EventType:     string(state),
		Payload:       raw,
		OccurredAt:    occurredAt.UTC(),
	}, nil
}

func parseSessionNumber(sessionID string) int {
	var number int
	_, _ = fmt.Sscanf(sessionID[len(sessionID)-10:], "s%d", &number)
	return number
}

func sentiment(rng *rand.Rand, positiveRatio float64) string {
	value := rng.Float64()
	if value < positiveRatio {
		return "positive"
	}
	if value < positiveRatio+(1-positiveRatio)*0.45 {
		return "neutral"
	}
	return "negative"
}

func commentIntent(rng *rand.Rand, persona Persona) string {
	if persona == PersonaCritic {
		return []string{"challenge", "ask_evidence", "negative_feedback"}[rng.Intn(3)]
	}
	if persona == PersonaKnowledgeUser || persona == PersonaCreator {
		return []string{"experience_share", "add_context", "answer_question"}[rng.Intn(3)]
	}
	return []string{"ask_link", "ask_price", "positive_feedback", "ask_usage"}[rng.Intn(4)]
}

func sessionStepDuration(rng *rand.Rand, next State) time.Duration {
	base := 2.0 + rng.ExpFloat64()*7
	if next == StateCommentCreated {
		base += 15 + rng.ExpFloat64()*20
	}
	return time.Duration(base * float64(time.Second))
}

func newReportAccumulator(cfg Config, dataset Dataset, profiles []UserProfile) *reportAccumulator {
	report := Report{
		RunID:            cfg.RunID,
		Profile:          cfg.Profile,
		Scenario:         cfg.Scenario,
		Seed:             cfg.Seed,
		Users:            len(dataset.UserIDs),
		Notes:            len(dataset.Notes),
		Sessions:         cfg.Sessions,
		SimulatedStartAt: cfg.StartAt,
		SimulatedEndAt:   cfg.StartAt.Add(cfg.Duration),
		PersonaCounts:    map[string]int64{},
		EventTypeCounts:  map[string]int64{},
		TransitionCounts: map[string]int64{},
		CategoryCounts:   map[string]int64{},
	}
	for _, profile := range profiles {
		report.PersonaCounts[string(profile.Persona)]++
	}
	return &reportAccumulator{report: report, noteEvents: map[int64]int64{}, userEvents: map[int64]int64{}}
}

func (a *reportAccumulator) record(event Event, previous State, category string, burst bool) {
	a.report.Events++
	a.report.EventTypeCounts[event.EventType]++
	transition := "start->" + event.EventType
	if previous != "" {
		transition = string(previous) + "->" + event.EventType
	}
	a.report.TransitionCounts[transition]++
	a.report.CategoryCounts[category]++
	a.noteEvents[event.NoteID]++
	a.userEvents[event.UserID]++
	if burst {
		a.burstEvents++
	}
}

func (a *reportAccumulator) finish(cfg Config, dataset Dataset) Report {
	a.report.AverageEventsPerSession = rounded(float64(a.report.Events) / float64(cfg.Sessions))
	a.report.BurstEventRatio = rounded(ratio(a.burstEvents, a.report.Events))
	a.report.P50EventsPerSession = percentile(a.sessionLengths, 0.50)
	a.report.P95EventsPerSession = percentile(a.sessionLengths, 0.95)
	a.report.TopNotes = topActivity(a.noteEvents, 20)
	a.report.TopUsers = topActivity(a.userEvents, 20)
	a.report.TopOnePercentNoteShare = rounded(topShare(a.noteEvents, len(dataset.Notes), 0.01))
	a.report.TopTenPercentUserShare = rounded(topShare(a.userEvents, len(dataset.UserIDs), 0.10))
	a.report.Checks = distributionChecks(a.report, cfg)
	return a.report
}

func distributionChecks(report Report, cfg Config) []DistributionCheck {
	personaDiversity := float64(len(report.PersonaCounts))
	eventTypeDiversity := float64(len(report.EventTypeCounts))
	checks := []DistributionCheck{
		{Name: "multi_step_sessions", Value: report.AverageEventsPerSession, Target: ">= 2.0", Passed: report.AverageEventsPerSession >= 2},
		{Name: "zipf_note_concentration", Value: report.TopOnePercentNoteShare, Target: "0.08 to 0.80", Passed: report.TopOnePercentNoteShare >= 0.08 && report.TopOnePercentNoteShare <= 0.80},
		{Name: "active_user_concentration", Value: report.TopTenPercentUserShare, Target: "0.20 to 0.80", Passed: report.TopTenPercentUserShare >= 0.20 && report.TopTenPercentUserShare <= 0.80},
		{Name: "persona_diversity", Value: personaDiversity, Target: ">= 6", Passed: personaDiversity >= 6},
		{Name: "event_type_diversity", Value: eventTypeDiversity, Target: ">= 8", Passed: eventTypeDiversity >= 8},
	}
	if cfg.Scenario == ScenarioOrganic {
		checks = append(checks, DistributionCheck{Name: "organic_burst_ratio", Value: report.BurstEventRatio, Target: "= 0", Passed: report.BurstEventRatio == 0})
	} else {
		minimum := cfg.BurstFraction * 0.65
		checks = append(checks, DistributionCheck{Name: "burst_visibility", Value: report.BurstEventRatio, Target: fmt.Sprintf(">= %.2f", minimum), Passed: report.BurstEventRatio >= minimum})
	}
	return checks
}

func topActivity(counts map[int64]int64, limit int) []ActivityRank {
	items := make([]ActivityRank, 0, len(counts))
	for id, count := range counts {
		items = append(items, ActivityRank{ID: id, Events: count})
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].Events == items[j].Events {
			return items[i].ID < items[j].ID
		}
		return items[i].Events > items[j].Events
	})
	if len(items) > limit {
		items = items[:limit]
	}
	return items
}

func topShare(counts map[int64]int64, population int, fraction float64) float64 {
	values := make([]int64, 0, len(counts))
	total := int64(0)
	for _, count := range counts {
		values = append(values, count)
		total += count
	}
	if total == 0 || population == 0 {
		return 0
	}
	sort.Slice(values, func(i, j int) bool { return values[i] > values[j] })
	take := max(1, int(math.Ceil(float64(population)*fraction)))
	if take > len(values) {
		take = len(values)
	}
	top := int64(0)
	for _, value := range values[:take] {
		top += value
	}
	return float64(top) / float64(total)
}

func percentile(values []int, quantile float64) int {
	if len(values) == 0 {
		return 0
	}
	copyOfValues := append([]int(nil), values...)
	sort.Ints(copyOfValues)
	index := int(math.Ceil(float64(len(copyOfValues))*quantile)) - 1
	return copyOfValues[max(0, min(index, len(copyOfValues)-1))]
}

func weightedIndex(rng *rand.Rand, weights []float64) int {
	total := 0.0
	for _, weight := range weights {
		if weight > 0 {
			total += weight
		}
	}
	if total <= 0 {
		return len(weights) - 1
	}
	needle := rng.Float64() * total
	for i, weight := range weights {
		if weight <= 0 {
			continue
		}
		needle -= weight
		if needle <= 0 {
			return i
		}
	}
	return len(weights) - 1
}

func boolWeight(enabled bool, weight float64) float64 {
	if !enabled {
		return 0
	}
	return weight
}

func ratio(value int64, total int64) float64 {
	if total == 0 {
		return 0
	}
	return float64(value) / float64(total)
}

func jitter(rng *rand.Rand, amount float64) float64 {
	return (rng.Float64()*2 - 1) * amount
}

func clamp(value float64, minimum float64, maximum float64) float64 {
	return math.Max(minimum, math.Min(maximum, value))
}

func rounded(value float64) float64 {
	return math.Round(value*10_000) / 10_000
}
