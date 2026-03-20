// Copyright (c) MyPal contributors. See LICENSE for details.

package heartbeat

import (
	"context"
	"errors"
	"time"

	domainmodels "github.com/BangRocket/MyPal/apps/backend/internal/domain/models"
	"github.com/BangRocket/MyPal/apps/backend/internal/domain/ports"
	"gorm.io/gorm"
)

type repository struct{ db *gorm.DB }

// NewHeartbeatRepository returns a HeartbeatRepositoryPort backed by the given *gorm.DB.
func NewHeartbeatRepository(db *gorm.DB) ports.HeartbeatRepositoryPort {
	return &repository{db: db}
}

func (r *repository) Create(ctx context.Context, item *domainmodels.HeartbeatItemModel) error {
	return r.db.WithContext(ctx).Create(item).Error
}

func (r *repository) GetByID(ctx context.Context, id string) (*domainmodels.HeartbeatItemModel, error) {
	var m domainmodels.HeartbeatItemModel
	err := r.db.WithContext(ctx).Where("id = ?", id).First(&m).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ports.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &m, nil
}

func (r *repository) ListActive(ctx context.Context) ([]domainmodels.HeartbeatItemModel, error) {
	var models []domainmodels.HeartbeatItemModel
	if err := r.db.WithContext(ctx).
		Where("status = ?", "active").
		Order("priority ASC, next_run ASC").
		Find(&models).Error; err != nil {
		return nil, err
	}
	return models, nil
}

func (r *repository) ListAll(ctx context.Context) ([]domainmodels.HeartbeatItemModel, error) {
	var models []domainmodels.HeartbeatItemModel
	if err := r.db.WithContext(ctx).
		Order("priority ASC, next_run ASC").
		Find(&models).Error; err != nil {
		return nil, err
	}
	return models, nil
}

func (r *repository) ListDue(ctx context.Context, now time.Time) ([]domainmodels.HeartbeatItemModel, error) {
	var models []domainmodels.HeartbeatItemModel
	if err := r.db.WithContext(ctx).
		Where("status = ? AND next_run <= ?", "active", now).
		Order("priority ASC").
		Find(&models).Error; err != nil {
		return nil, err
	}
	return models, nil
}

func (r *repository) Update(ctx context.Context, item *domainmodels.HeartbeatItemModel) error {
	return r.db.WithContext(ctx).Save(item).Error
}

func (r *repository) Delete(ctx context.Context, id string) error {
	return r.db.WithContext(ctx).Delete(&domainmodels.HeartbeatItemModel{}, "id = ?", id).Error
}

func (r *repository) AddLog(ctx context.Context, log *domainmodels.HeartbeatLogModel) error {
	return r.db.WithContext(ctx).Create(log).Error
}

func (r *repository) GetLogs(ctx context.Context, itemID string, limit int) ([]domainmodels.HeartbeatLogModel, error) {
	var models []domainmodels.HeartbeatLogModel
	if err := r.db.WithContext(ctx).
		Where("heartbeat_item_id = ?", itemID).
		Order("timestamp DESC").
		Limit(limit).
		Find(&models).Error; err != nil {
		return nil, err
	}
	return models, nil
}
