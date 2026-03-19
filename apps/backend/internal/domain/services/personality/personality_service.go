// Copyright (c) MyPal contributors. See LICENSE for details.

package personality

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/BangRocket/MyPal/apps/backend/internal/domain/models"
	"github.com/BangRocket/MyPal/apps/backend/internal/domain/ports"
	"github.com/google/uuid"
)

// Service provides domain operations for personality management including
// CRUD, relationship tracking, and prompt assembly.
type Service struct {
	personalityRepo ports.PersonalityRepositoryPort
	relationshipRepo ports.UserPersonaRelationshipRepositoryPort
}

// NewService constructs a personality Service.
func NewService(
	pRepo ports.PersonalityRepositoryPort,
	rRepo ports.UserPersonaRelationshipRepositoryPort,
) *Service {
	return &Service{
		personalityRepo:  pRepo,
		relationshipRepo: rRepo,
	}
}

// ---------------------------------------------------------------------------
// CRUD — delegate to personality repository
// ---------------------------------------------------------------------------

// Create persists a new personality, generating a UUID if the ID is empty.
func (s *Service) Create(ctx context.Context, p *models.PersonalityModel) error {
	if p.ID == "" {
		p.ID = uuid.New().String()
	}
	return s.personalityRepo.Create(ctx, p)
}

// Get returns a personality by ID.
func (s *Service) Get(ctx context.Context, id string) (*models.PersonalityModel, error) {
	return s.personalityRepo.GetByID(ctx, id)
}

// List returns all personalities.
func (s *Service) List(ctx context.Context) ([]models.PersonalityModel, error) {
	return s.personalityRepo.List(ctx)
}

// Update persists changes to an existing personality.
func (s *Service) Update(ctx context.Context, p *models.PersonalityModel) error {
	return s.personalityRepo.Update(ctx, p)
}

// Delete removes a personality by ID.
func (s *Service) Delete(ctx context.Context, id string) error {
	return s.personalityRepo.Delete(ctx, id)
}

// SetDefault marks a personality as the default (and unsets all others).
func (s *Service) SetDefault(ctx context.Context, id string) error {
	return s.personalityRepo.SetDefault(ctx, id)
}

// ---------------------------------------------------------------------------
// Relationships
// ---------------------------------------------------------------------------

// GetRelationship returns (or creates) the relationship between a user and a personality.
func (s *Service) GetRelationship(ctx context.Context, userID, personalityID string) (*models.UserPersonaRelationshipModel, error) {
	return s.relationshipRepo.GetOrCreate(ctx, userID, personalityID)
}

// GetUserRelationships returns all relationships for a given user.
func (s *Service) GetUserRelationships(ctx context.Context, userID string) ([]models.UserPersonaRelationshipModel, error) {
	return s.relationshipRepo.GetByUser(ctx, userID)
}

// RecordInteraction increments the interaction count (and familiarity) for the user/personality pair.
func (s *Service) RecordInteraction(ctx context.Context, userID, personalityID string) error {
	return s.relationshipRepo.IncrementInteraction(ctx, userID, personalityID)
}

// ---------------------------------------------------------------------------
// Prompt building
// ---------------------------------------------------------------------------

// BuildPersonalityPrompt assembles a system prompt from the personality config
// and the user's familiarity level. If personalityID is empty the default
// personality is used. channelType (e.g. "telegram", "discord") selects a
// channel-specific adaptation when present in the personality's adaptations map.
func (s *Service) BuildPersonalityPrompt(ctx context.Context, personalityID, userID, channelType string) (string, error) {
	// 1. Load personality.
	var p *models.PersonalityModel
	var err error
	if personalityID != "" {
		p, err = s.personalityRepo.GetByID(ctx, personalityID)
	} else {
		p, err = s.personalityRepo.GetDefault(ctx)
	}
	if err != nil {
		return "", fmt.Errorf("personality: load: %w", err)
	}

	// 2. Load (or create) user relationship.
	rel, err := s.relationshipRepo.GetOrCreate(ctx, userID, p.ID)
	if err != nil {
		return "", fmt.Errorf("personality: relationship: %w", err)
	}

	// 3. Parse JSON fields.
	traits, err := parseJSONStringArray(p.Traits)
	if err != nil {
		return "", fmt.Errorf("personality: parse traits: %w", err)
	}
	boundaries, err := parseJSONStringArray(p.Boundaries)
	if err != nil {
		return "", fmt.Errorf("personality: parse boundaries: %w", err)
	}
	quirks, err := parseJSONStringArray(p.Quirks)
	if err != nil {
		return "", fmt.Errorf("personality: parse quirks: %w", err)
	}
	adaptations, err := parseJSONStringMap(p.Adaptations)
	if err != nil {
		return "", fmt.Errorf("personality: parse adaptations: %w", err)
	}

	// 4. Build prompt string.
	var b strings.Builder
	b.WriteString(p.BasePrompt)

	if len(traits) > 0 {
		b.WriteString("\n\nYour personality traits: ")
		b.WriteString(strings.Join(traits, ", "))
	}

	if p.Tone != "" {
		b.WriteString("\n\nYour tone: ")
		b.WriteString(p.Tone)
	}

	if len(boundaries) > 0 {
		b.WriteString("\n\nBoundaries (things you must not do): ")
		b.WriteString(strings.Join(boundaries, ", "))
	}

	if len(quirks) > 0 {
		b.WriteString("\n\nQuirks: ")
		b.WriteString(strings.Join(quirks, ", "))
	}

	if channelType != "" {
		if adaptation, ok := adaptations[channelType]; ok {
			b.WriteString("\n\nChannel-specific guidance (")
			b.WriteString(channelType)
			b.WriteString("): ")
			b.WriteString(adaptation)
		}
	}

	// 5. Familiarity adjustments.
	switch {
	case rel.Familiarity < 0.3:
		b.WriteString("\n\nYou don't know this user well yet. Be polite and somewhat formal. Don't assume familiarity.")
	case rel.Familiarity <= 0.7:
		b.WriteString("\n\nYou're getting to know this user. You can be more casual and reference shared context.")
	default:
		b.WriteString("\n\nYou know this user well. Be casual, warm, and feel free to show personality. Reference your shared history.")
	}

	return b.String(), nil
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

// parseJSONStringArray decodes a JSON array of strings. Returns nil (no error)
// for empty input.
func parseJSONStringArray(raw string) ([]string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "null" {
		return nil, nil
	}
	var out []string
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil, err
	}
	return out, nil
}

// parseJSONStringMap decodes a JSON object mapping string keys to string values.
// Returns nil (no error) for empty input.
func parseJSONStringMap(raw string) (map[string]string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "null" {
		return nil, nil
	}
	var out map[string]string
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil, err
	}
	return out, nil
}
