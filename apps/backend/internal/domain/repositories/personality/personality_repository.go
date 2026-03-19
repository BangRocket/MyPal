// Copyright (c) MyPal contributors. See LICENSE for details.

package personality

import (
	"context"
	"errors"

	domainmodels "github.com/BangRocket/MyPal/apps/backend/internal/domain/models"
	"github.com/BangRocket/MyPal/apps/backend/internal/domain/ports"
	"gorm.io/gorm"
)

type repository struct{ db *gorm.DB }

// NewPersonalityRepository returns a PersonalityRepositoryPort backed by the given *gorm.DB.
func NewPersonalityRepository(db *gorm.DB) ports.PersonalityRepositoryPort {
	return &repository{db: db}
}

func (r *repository) Create(ctx context.Context, p *domainmodels.PersonalityModel) error {
	return r.db.WithContext(ctx).Create(p).Error
}

func (r *repository) GetByID(ctx context.Context, id string) (*domainmodels.PersonalityModel, error) {
	var m domainmodels.PersonalityModel
	err := r.db.WithContext(ctx).Where("id = ?", id).First(&m).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ports.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &m, nil
}

func (r *repository) GetDefault(ctx context.Context) (*domainmodels.PersonalityModel, error) {
	var m domainmodels.PersonalityModel
	err := r.db.WithContext(ctx).Where("is_default = ?", true).First(&m).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ports.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &m, nil
}

func (r *repository) List(ctx context.Context) ([]domainmodels.PersonalityModel, error) {
	var models []domainmodels.PersonalityModel
	if err := r.db.WithContext(ctx).Order("created_at ASC").Find(&models).Error; err != nil {
		return nil, err
	}
	return models, nil
}

func (r *repository) Update(ctx context.Context, p *domainmodels.PersonalityModel) error {
	return r.db.WithContext(ctx).Save(p).Error
}

func (r *repository) Delete(ctx context.Context, id string) error {
	return r.db.WithContext(ctx).Delete(&domainmodels.PersonalityModel{}, "id = ?", id).Error
}

func (r *repository) SetDefault(ctx context.Context, id string) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Unset all existing defaults.
		if err := tx.Model(&domainmodels.PersonalityModel{}).
			Where("is_default = ?", true).
			Update("is_default", false).Error; err != nil {
			return err
		}
		// Set the requested personality as default.
		return tx.Model(&domainmodels.PersonalityModel{}).
			Where("id = ?", id).
			Update("is_default", true).Error
	})
}
