// Copyright (c) MyPal contributors. See LICENSE for details.

package organic

import (
	"context"
	"errors"
	"time"

	domainmodels "github.com/BangRocket/MyPal/apps/backend/internal/domain/models"
	"github.com/BangRocket/MyPal/apps/backend/internal/domain/ports"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

type repository struct{ db *gorm.DB }

// NewOrganicResponseConfigRepository returns an OrganicResponseConfigRepositoryPort backed by the given *gorm.DB.
func NewOrganicResponseConfigRepository(db *gorm.DB) ports.OrganicResponseConfigRepositoryPort {
	return &repository{db: db}
}

func (r *repository) GetByChannel(ctx context.Context, channelID string) (*domainmodels.OrganicResponseConfigModel, error) {
	var m domainmodels.OrganicResponseConfigModel
	err := r.db.WithContext(ctx).Where("channel_id = ?", channelID).First(&m).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ports.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &m, nil
}

func (r *repository) Upsert(ctx context.Context, cfg *domainmodels.OrganicResponseConfigModel) error {
	now := time.Now().UTC()
	cfg.UpdatedAt = now

	// Try to find existing record by channel_id.
	var existing domainmodels.OrganicResponseConfigModel
	err := r.db.WithContext(ctx).Where("channel_id = ?", cfg.ChannelID).First(&existing).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		// Insert new record.
		if cfg.ID == "" {
			cfg.ID = uuid.New().String()
		}
		cfg.CreatedAt = now
		return r.db.WithContext(ctx).Create(cfg).Error
	}
	if err != nil {
		return err
	}

	// Update existing record.
	cfg.ID = existing.ID
	cfg.CreatedAt = existing.CreatedAt
	return r.db.WithContext(ctx).Save(cfg).Error
}
