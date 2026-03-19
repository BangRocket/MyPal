// Copyright (c) MyPal contributors. See LICENSE for details.

package models

import "time"

// SandboxInstanceModel tracks sandbox instances in the database.
type SandboxInstanceModel struct {
	ID         string    `gorm:"primaryKey;column:id;type:text"`
	Image      string    `gorm:"column:image;type:text;not null"`
	Status     string    `gorm:"column:status;type:text;default:'creating'"`
	UserID     string    `gorm:"column:user_id;type:text;not null;index:idx_sandbox_user"`
	MemLimit   int64     `gorm:"column:mem_limit"`
	CPULimit   float64   `gorm:"column:cpu_limit"`
	NetPolicy  string    `gorm:"column:net_policy;type:text;default:'none'"`
	Persistent bool      `gorm:"column:persistent;default:false"`
	CreatedAt  time.Time `gorm:"column:created_at;autoCreateTime:false"`
	UpdatedAt  time.Time `gorm:"column:updated_at;autoUpdateTime:false"`
}

// TableName overrides the default GORM table name.
func (SandboxInstanceModel) TableName() string { return "sandbox_instances" }
