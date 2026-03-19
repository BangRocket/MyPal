// Copyright (c) MyPal contributors. See LICENSE for details.

package personality

import (
	"context"
	"time"

	"github.com/google/uuid"

	domainmodels "github.com/BangRocket/MyPal/apps/backend/internal/domain/models"
	"github.com/BangRocket/MyPal/apps/backend/internal/domain/ports"
	"gorm.io/gorm"
)

type relationshipRepository struct{ db *gorm.DB }

// NewRelationshipRepository returns a UserPersonaRelationshipRepositoryPort backed by the given *gorm.DB.
func NewRelationshipRepository(db *gorm.DB) ports.UserPersonaRelationshipRepositoryPort {
	return &relationshipRepository{db: db}
}

func (r *relationshipRepository) GetOrCreate(ctx context.Context, userID, personalityID string) (*domainmodels.UserPersonaRelationshipModel, error) {
	var m domainmodels.UserPersonaRelationshipModel
	err := r.db.WithContext(ctx).
		Where("user_id = ? AND personality_id = ?", userID, personalityID).
		First(&m).Error
	if err == nil {
		return &m, nil
	}
	if err != gorm.ErrRecordNotFound {
		return nil, err
	}
	now := time.Now().UTC()
	m = domainmodels.UserPersonaRelationshipModel{
		ID:               uuid.New().String(),
		UserID:           userID,
		PersonalityID:    personalityID,
		Familiarity:      0.0,
		InteractionCount: 0,
		CreatedAt:        now,
		UpdatedAt:        now,
	}
	if err := r.db.WithContext(ctx).Create(&m).Error; err != nil {
		return nil, err
	}
	return &m, nil
}

func (r *relationshipRepository) Update(ctx context.Context, rel *domainmodels.UserPersonaRelationshipModel) error {
	return r.db.WithContext(ctx).Save(rel).Error
}

func (r *relationshipRepository) GetByUser(ctx context.Context, userID string) ([]domainmodels.UserPersonaRelationshipModel, error) {
	var models []domainmodels.UserPersonaRelationshipModel
	if err := r.db.WithContext(ctx).Where("user_id = ?", userID).Find(&models).Error; err != nil {
		return nil, err
	}
	return models, nil
}

func (r *relationshipRepository) IncrementInteraction(ctx context.Context, userID, personalityID string) error {
	now := time.Now().UTC()
	return r.db.WithContext(ctx).Exec(
		`UPDATE user_persona_relationships
		 SET interaction_count = interaction_count + 1,
		     last_interaction  = ?,
		     familiarity       = MIN(1.0, familiarity + 0.01 * (1.0 - familiarity)),
		     updated_at        = ?
		 WHERE user_id = ? AND personality_id = ?`,
		now, now, userID, personalityID,
	).Error
}
