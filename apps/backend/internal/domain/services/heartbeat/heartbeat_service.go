// Copyright (c) MyPal contributors. See LICENSE for details.

// Package heartbeat provides a domain service for managing recurring check-ins
// and scheduled actions ("heartbeats"). It includes CRUD operations, bot
// self-modification methods, and an evaluation loop that dispatches due
// heartbeats through the agentic pipeline.
package heartbeat

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/BangRocket/MyPal/apps/backend/internal/domain/models"
	"github.com/BangRocket/MyPal/apps/backend/internal/domain/ports"
	"github.com/google/uuid"
)

// DefaultInterval is the evaluation loop tick interval when none is specified.
const DefaultInterval = 15 * time.Minute

// Service manages heartbeat items and runs the evaluation loop.
type Service struct {
	repo       ports.HeartbeatRepositoryPort
	dispatcher ports.TaskDispatcherPort
	interval   time.Duration
	notifyCh   chan struct{}
}

// NewService constructs a heartbeat Service. If interval is zero or negative,
// DefaultInterval (15 min) is used.
func NewService(
	repo ports.HeartbeatRepositoryPort,
	dispatcher ports.TaskDispatcherPort,
	interval time.Duration,
) *Service {
	if interval <= 0 {
		interval = DefaultInterval
	}
	return &Service{
		repo:       repo,
		dispatcher: dispatcher,
		interval:   interval,
		notifyCh:   make(chan struct{}, 1),
	}
}

// SetDispatcher allows setting or replacing the task dispatcher after
// construction. This is useful when the dispatcher depends on components
// that are initialised after the heartbeat service.
func (s *Service) SetDispatcher(d ports.TaskDispatcherPort) {
	s.dispatcher = d
}

// ---------------------------------------------------------------------------
// CRUD Methods
// ---------------------------------------------------------------------------

// Create generates a UUID, sets timestamps, computes NextRun from Schedule,
// persists the item, and logs a "created" action.
func (s *Service) Create(ctx context.Context, item *models.HeartbeatItemModel) error {
	now := time.Now()
	item.ID = uuid.NewString()
	item.CreatedAt = now
	item.UpdatedAt = now
	item.Status = "active"
	item.NextRun = computeNextRun(item.Schedule, now)

	if err := s.repo.Create(ctx, item); err != nil {
		return fmt.Errorf("heartbeat: create: %w", err)
	}
	s.addLog(ctx, item.ID, "created", "", "")
	log.Printf("heartbeat: created item %s %q next_run=%s", item.ID, item.Title, item.NextRun.Format(time.RFC3339))
	return nil
}

// Get retrieves a heartbeat item by ID.
func (s *Service) Get(ctx context.Context, id string) (*models.HeartbeatItemModel, error) {
	return s.repo.GetByID(ctx, id)
}

// List returns all active heartbeat items.
func (s *Service) List(ctx context.Context) ([]models.HeartbeatItemModel, error) {
	return s.repo.ListActive(ctx)
}

// ListAll returns all heartbeat items regardless of status.
func (s *Service) ListAll(ctx context.Context) ([]models.HeartbeatItemModel, error) {
	return s.repo.ListAll(ctx)
}

// Delete removes a heartbeat item by ID.
func (s *Service) Delete(ctx context.Context, id string) error {
	return s.repo.Delete(ctx, id)
}

// Logs returns execution logs for a heartbeat item.
func (s *Service) Logs(ctx context.Context, itemID string, limit int) ([]models.HeartbeatLogModel, error) {
	if limit <= 0 {
		limit = 50
	}
	return s.repo.GetLogs(ctx, itemID, limit)
}

// Update modifies fields on an existing heartbeat item, recomputes NextRun,
// and logs a "modified" action.
func (s *Service) Update(ctx context.Context, id string, item *models.HeartbeatItemModel) error {
	existing, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return fmt.Errorf("heartbeat: update: %w", err)
	}

	now := time.Now()
	existing.Title = item.Title
	existing.Description = item.Description
	existing.Schedule = item.Schedule
	existing.Priority = item.Priority
	existing.TargetUser = item.TargetUser
	existing.TargetChannel = item.TargetChannel
	existing.Context = item.Context
	existing.UpdatedAt = now
	existing.NextRun = computeNextRun(existing.Schedule, now)

	if err := s.repo.Update(ctx, existing); err != nil {
		return fmt.Errorf("heartbeat: update: %w", err)
	}
	s.addLog(ctx, id, "modified", "", "")
	log.Printf("heartbeat: updated item %s %q next_run=%s", id, existing.Title, existing.NextRun.Format(time.RFC3339))
	return nil
}

// Snooze sets a heartbeat item to snoozed status with NextRun = until.
func (s *Service) Snooze(ctx context.Context, id string, until time.Time) error {
	existing, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return fmt.Errorf("heartbeat: snooze: %w", err)
	}

	existing.Status = "snoozed"
	existing.NextRun = until
	existing.UpdatedAt = time.Now()

	if err := s.repo.Update(ctx, existing); err != nil {
		return fmt.Errorf("heartbeat: snooze: %w", err)
	}
	s.addLog(ctx, id, "snoozed", "", "")
	log.Printf("heartbeat: snoozed item %s until %s", id, until.Format(time.RFC3339))
	return nil
}

// Complete marks a heartbeat item as completed.
func (s *Service) Complete(ctx context.Context, id string) error {
	existing, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return fmt.Errorf("heartbeat: complete: %w", err)
	}

	existing.Status = "completed"
	existing.UpdatedAt = time.Now()

	if err := s.repo.Update(ctx, existing); err != nil {
		return fmt.Errorf("heartbeat: complete: %w", err)
	}
	s.addLog(ctx, id, "completed", "", "")
	log.Printf("heartbeat: completed item %s", id)
	return nil
}

// Cancel marks a heartbeat item as cancelled.
func (s *Service) Cancel(ctx context.Context, id string) error {
	existing, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return fmt.Errorf("heartbeat: cancel: %w", err)
	}

	existing.Status = "cancelled"
	existing.UpdatedAt = time.Now()

	if err := s.repo.Update(ctx, existing); err != nil {
		return fmt.Errorf("heartbeat: cancel: %w", err)
	}
	s.addLog(ctx, id, "cancelled", "", "")
	log.Printf("heartbeat: cancelled item %s", id)
	return nil
}

// ---------------------------------------------------------------------------
// Bot Self-Modification Methods
// ---------------------------------------------------------------------------

// BotCreate creates a heartbeat item on behalf of the bot, recording the reason.
func (s *Service) BotCreate(ctx context.Context, item *models.HeartbeatItemModel, reason string) error {
	item.CreatedBy = "bot"
	if err := s.Create(ctx, item); err != nil {
		return err
	}
	// Overwrite the "created" log with one that includes the reason.
	s.addLog(ctx, item.ID, "created", reason, "")
	log.Printf("heartbeat: bot created item %s reason=%q", item.ID, reason)
	return nil
}

// BotComplete marks a heartbeat item as completed on behalf of the bot.
func (s *Service) BotComplete(ctx context.Context, id string, reason string) error {
	if err := s.Complete(ctx, id); err != nil {
		return err
	}
	s.addLog(ctx, id, "bot_completed", reason, "")
	log.Printf("heartbeat: bot completed item %s reason=%q", id, reason)
	return nil
}

// BotSnooze snoozes a heartbeat item on behalf of the bot.
func (s *Service) BotSnooze(ctx context.Context, id string, until time.Time, reason string) error {
	if err := s.Snooze(ctx, id, until); err != nil {
		return err
	}
	s.addLog(ctx, id, "bot_snoozed", reason, "")
	log.Printf("heartbeat: bot snoozed item %s until %s reason=%q", id, until.Format(time.RFC3339), reason)
	return nil
}

// ---------------------------------------------------------------------------
// Evaluation Loop
// ---------------------------------------------------------------------------

// Run starts the evaluation loop. It blocks until ctx is cancelled.
func (s *Service) Run(ctx context.Context) {
	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()

	log.Printf("heartbeat: evaluation loop started (interval=%s)", s.interval)

	for {
		select {
		case <-ctx.Done():
			log.Println("heartbeat: evaluation loop stopped")
			return
		case <-ticker.C:
			s.evaluate(ctx)
		case <-s.notifyCh:
			s.evaluate(ctx)
		}
	}
}

// Notify wakes the evaluation loop so it runs an immediate evaluation cycle.
func (s *Service) Notify() {
	select {
	case s.notifyCh <- struct{}{}:
	default:
	}
}

func (s *Service) evaluate(ctx context.Context) {
	now := time.Now()
	items, err := s.repo.ListDue(ctx, now)
	if err != nil {
		log.Printf("heartbeat: evaluate: list due error: %v", err)
		return
	}
	if len(items) == 0 {
		return
	}

	log.Printf("heartbeat: evaluate: %d item(s) due", len(items))

	for i := range items {
		item := &items[i]

		prompt := fmt.Sprintf("[Heartbeat] %s\nDescription: %s\nTarget: %s via %s\nContext: %s\nPriority: %d/5",
			item.Title, item.Description, item.TargetUser, item.TargetChannel, item.Context, item.Priority)

		err := s.dispatcher.Dispatch(ctx, prompt)
		result := "ok"
		if err != nil {
			result = fmt.Sprintf("error: %v", err)
			log.Printf("heartbeat: dispatch error for item %s: %v", item.ID, err)
		}

		// Update last_run and compute next_run.
		item.LastRun = now
		item.UpdatedAt = now

		if isOneShotSchedule(item.Schedule) {
			item.Status = "completed"
			log.Printf("heartbeat: one-shot item %s completed", item.ID)
		} else {
			item.NextRun = computeNextRun(item.Schedule, now)
		}

		if updateErr := s.repo.Update(ctx, item); updateErr != nil {
			log.Printf("heartbeat: failed to update item %s after execution: %v", item.ID, updateErr)
		}

		s.addLog(ctx, item.ID, "executed", "", result)
		log.Printf("heartbeat: executed item %s %q result=%s next_run=%s",
			item.ID, item.Title, result, item.NextRun.Format(time.RFC3339))
	}
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func (s *Service) addLog(ctx context.Context, itemID, action, reason, result string) {
	entry := &models.HeartbeatLogModel{
		ID:              uuid.NewString(),
		HeartbeatItemID: itemID,
		Action:          action,
		Reason:          reason,
		Result:          result,
		Timestamp:       time.Now(),
	}
	if err := s.repo.AddLog(ctx, entry); err != nil {
		log.Printf("heartbeat: failed to add log for item %s action=%s: %v", itemID, action, err)
	}
}

// computeNextRun determines the next execution time for a schedule string.
//   - Empty string: one-shot, execute on next evaluation (NextRun = now).
//   - RFC3339 datetime: one-shot at that time.
//   - Cron expression (5 fields): next occurrence after now.
func computeNextRun(schedule string, now time.Time) time.Time {
	if schedule == "" {
		return now
	}

	// Try RFC3339 first.
	if t, err := time.Parse(time.RFC3339, schedule); err == nil {
		return t
	}

	// Try cron expression.
	return nextCronRun(schedule, now)
}

// isOneShotSchedule returns true for empty or datetime schedules (no recurrence).
func isOneShotSchedule(schedule string) bool {
	if schedule == "" {
		return true
	}
	_, err := time.Parse(time.RFC3339, schedule)
	return err == nil
}

// nextCronRun computes the next time a 5-field cron expression fires after
// the given time. This mirrors the scheduler's cron logic.
func nextCronRun(schedule string, after time.Time) time.Time {
	fields := splitCronFields(schedule)
	if len(fields) != 5 {
		return after.Add(time.Hour)
	}

	candidate := after.Truncate(time.Minute).Add(time.Minute)
	deadline := after.Add(366 * 24 * time.Hour)

	for candidate.Before(deadline) {
		if cronFieldMatches(fields[1], candidate.Hour()) &&
			cronFieldMatches(fields[0], candidate.Minute()) &&
			cronFieldMatches(fields[2], candidate.Day()) &&
			cronFieldMatches(fields[3], int(candidate.Month())) &&
			cronFieldMatches(fields[4], int(candidate.Weekday())) {
			return candidate
		}
		candidate = candidate.Add(time.Minute)
	}
	return after.Add(time.Hour)
}

func splitCronFields(s string) []string {
	var fields []string
	cur := ""
	for _, ch := range s {
		if ch == ' ' || ch == '\t' {
			if cur != "" {
				fields = append(fields, cur)
				cur = ""
			}
		} else {
			cur += string(ch)
		}
	}
	if cur != "" {
		fields = append(fields, cur)
	}
	return fields
}

func cronFieldMatches(f string, value int) bool {
	if f == "*" {
		return true
	}
	if len(f) > 2 && f[:2] == "*/" {
		var step int
		if _, err := fmt.Sscanf(f[2:], "%d", &step); err == nil && step > 0 {
			return value%step == 0
		}
		return false
	}
	var n int
	if _, err := fmt.Sscanf(f, "%d", &n); err == nil {
		return n == value
	}
	return false
}
