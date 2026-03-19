// Copyright (c) MyPal contributors. See LICENSE for details.

package sandbox

import (
	"context"
	"errors"

	domainmodels "github.com/BangRocket/MyPal/apps/backend/internal/domain/models"
	"github.com/BangRocket/MyPal/apps/backend/internal/domain/ports"
	"gorm.io/gorm"
)

type repository struct{ db *gorm.DB }

// NewSandboxRepository returns a SandboxRepositoryPort backed by the given *gorm.DB.
func NewSandboxRepository(db *gorm.DB) ports.SandboxRepositoryPort {
	return &repository{db: db}
}

func (r *repository) Create(ctx context.Context, instance *domainmodels.SandboxInstanceModel) error {
	return r.db.WithContext(ctx).Create(instance).Error
}

func (r *repository) GetByID(ctx context.Context, id string) (*domainmodels.SandboxInstanceModel, error) {
	var m domainmodels.SandboxInstanceModel
	err := r.db.WithContext(ctx).Where("id = ?", id).First(&m).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ports.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &m, nil
}

func (r *repository) List(ctx context.Context) ([]domainmodels.SandboxInstanceModel, error) {
	var models []domainmodels.SandboxInstanceModel
	if err := r.db.WithContext(ctx).
		Order("created_at DESC").
		Find(&models).Error; err != nil {
		return nil, err
	}
	return models, nil
}

func (r *repository) ListByUser(ctx context.Context, userID string) ([]domainmodels.SandboxInstanceModel, error) {
	var models []domainmodels.SandboxInstanceModel
	if err := r.db.WithContext(ctx).
		Where("user_id = ?", userID).
		Order("created_at DESC").
		Find(&models).Error; err != nil {
		return nil, err
	}
	return models, nil
}

func (r *repository) UpdateStatus(ctx context.Context, id, status string) error {
	result := r.db.WithContext(ctx).
		Model(&domainmodels.SandboxInstanceModel{}).
		Where("id = ?", id).
		Update("status", status)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ports.ErrNotFound
	}
	return nil
}

func (r *repository) Delete(ctx context.Context, id string) error {
	result := r.db.WithContext(ctx).
		Delete(&domainmodels.SandboxInstanceModel{}, "id = ?", id)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ports.ErrNotFound
	}
	return nil
}
