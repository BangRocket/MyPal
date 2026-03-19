// Copyright (c) MyPal contributors. See LICENSE for details.

package organic

import (
	"context"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/BangRocket/MyPal/apps/backend/internal/domain/ports"
)

// Service decides whether the bot should organically respond to group
// messages it was not directly mentioned in.
type Service struct {
	configRepo   ports.OrganicResponseConfigRepositoryPort
	botName      string
	lastResponse map[string]time.Time  // channelID -> last organic response time
	dailyCount   map[string]dailyEntry // channelID -> {count, date}
	mu           sync.RWMutex
}

type dailyEntry struct {
	count int
	date  string // "2006-01-02"
}

// OrganicInput carries the data needed to decide on an organic response.
type OrganicInput struct {
	ChannelID   string
	Message     string
	SenderName  string
	IsGroup     bool
	IsMentioned bool
}

// NewService constructs an organic response Service.
func NewService(configRepo ports.OrganicResponseConfigRepositoryPort, botName string) *Service {
	return &Service{
		configRepo:   configRepo,
		botName:      strings.ToLower(botName),
		lastResponse: make(map[string]time.Time),
		dailyCount:   make(map[string]dailyEntry),
	}
}

// ShouldRespond decides whether the bot should organically respond to a
// group message. It uses simple heuristics (not LLM calls) to keep costs
// low for every group message.
func (s *Service) ShouldRespond(ctx context.Context, input OrganicInput) bool {
	// 1. Not a group message → false.
	if !input.IsGroup {
		return false
	}

	// 2. Mentioned → true (bypass all checks).
	if input.IsMentioned {
		return true
	}

	// 3. Load channel config; if not enabled → false.
	cfg, err := s.configRepo.GetByChannel(ctx, input.ChannelID)
	if err != nil {
		// No config or error → not enabled.
		return false
	}
	if !cfg.Enabled {
		return false
	}

	// 4. Check quiet hours.
	if s.inQuietHours(cfg.QuietHoursStart, cfg.QuietHoursEnd) {
		return false
	}

	// 5. Check cooldown.
	s.mu.RLock()
	lastResp, hasLast := s.lastResponse[input.ChannelID]
	s.mu.RUnlock()
	if hasLast {
		cooldown := time.Duration(cfg.CooldownSeconds) * time.Second
		if time.Since(lastResp) < cooldown {
			return false
		}
	}

	// 6. Check daily limit.
	today := time.Now().Format("2006-01-02")
	s.mu.RLock()
	daily := s.dailyCount[input.ChannelID]
	s.mu.RUnlock()
	if daily.date == today && daily.count >= cfg.MaxDailyOrganic {
		return false
	}

	// 7. Simple keyword heuristic for relevance scoring.
	score := s.computeRelevanceScore(input.Message)
	if score < cfg.RelevanceThreshold {
		return false
	}

	// 8. All checks passed — record the response and increment counters.
	s.mu.Lock()
	s.lastResponse[input.ChannelID] = time.Now()
	if daily.date != today {
		s.dailyCount[input.ChannelID] = dailyEntry{count: 1, date: today}
	} else {
		s.dailyCount[input.ChannelID] = dailyEntry{count: daily.count + 1, date: today}
	}
	s.mu.Unlock()

	log.Printf("organic: responding to group message in channel %s (score=%.2f, threshold=%.2f)",
		input.ChannelID, score, cfg.RelevanceThreshold)

	return true
}

// computeRelevanceScore returns a 0.0–1.0 score using simple keyword
// heuristics. This is intentionally cheap — no LLM call.
func (s *Service) computeRelevanceScore(message string) float64 {
	lower := strings.ToLower(message)
	score := 0.0

	// Bot name mentioned in message body.
	if s.botName != "" && strings.Contains(lower, s.botName) {
		score += 0.5
	}

	// Help-seeking keywords.
	helpKeywords := []string{"help", "anyone know", "does anyone", "can someone", "how do i", "how to"}
	for _, kw := range helpKeywords {
		if strings.Contains(lower, kw) {
			score += 0.3
			break
		}
	}

	// Direct question (contains question mark).
	if strings.Contains(message, "?") {
		score += 0.2
	}

	// Greeting or attention-seeking patterns.
	greetings := []string{"hey everyone", "hey all", "hello everyone", "hi all", "good morning", "good afternoon"}
	for _, g := range greetings {
		if strings.Contains(lower, g) {
			score += 0.1
			break
		}
	}

	// Cap at 1.0.
	if score > 1.0 {
		score = 1.0
	}

	return score
}

// inQuietHours checks whether the current local time falls within the
// configured quiet hours window. Times are parsed as "15:04" format.
func (s *Service) inQuietHours(startStr, endStr string) bool {
	if startStr == "" || endStr == "" {
		return false
	}

	now := time.Now()
	startTime, err := time.Parse("15:04", startStr)
	if err != nil {
		return false
	}
	endTime, err := time.Parse("15:04", endStr)
	if err != nil {
		return false
	}

	// Normalize to today's date for comparison.
	nowMinutes := now.Hour()*60 + now.Minute()
	startMinutes := startTime.Hour()*60 + startTime.Minute()
	endMinutes := endTime.Hour()*60 + endTime.Minute()

	if startMinutes <= endMinutes {
		// Same-day window: e.g. 22:00–06:00 does NOT apply here.
		// e.g. 09:00–17:00
		return nowMinutes >= startMinutes && nowMinutes < endMinutes
	}
	// Overnight window: e.g. 22:00–06:00
	return nowMinutes >= startMinutes || nowMinutes < endMinutes
}
