// Copyright (c) MyPal contributors. See LICENSE for details.

package models

import "time"

// HeartbeatItemModel represents a recurring check-in or scheduled action.
type HeartbeatItemModel struct {
	ID            string    `gorm:"primaryKey;column:id;type:text"`
	Title         string    `gorm:"column:title;type:text;not null"`
	Description   string    `gorm:"column:description;type:text"`
	Schedule      string    `gorm:"column:schedule;type:text"`
	Priority      int       `gorm:"column:priority;default:3"`
	Status        string    `gorm:"column:status;type:text;default:'active'"`
	CreatedBy     string    `gorm:"column:created_by;type:text"`
	TargetUser    string    `gorm:"column:target_user;type:text"`
	TargetChannel string    `gorm:"column:target_channel;type:text"`
	Context       string    `gorm:"column:context;type:text"`
	LastRun       time.Time `gorm:"column:last_run"`
	NextRun       time.Time `gorm:"column:next_run;index:idx_hb_next_run"`
	CreatedAt     time.Time `gorm:"column:created_at;autoCreateTime:false"`
	UpdatedAt     time.Time `gorm:"column:updated_at;autoUpdateTime:false"`
}

func (HeartbeatItemModel) TableName() string { return "heartbeat_items" }

// HeartbeatLogModel records actions taken for a heartbeat item.
type HeartbeatLogModel struct {
	ID              string    `gorm:"primaryKey;column:id;type:text"`
	HeartbeatItemID string    `gorm:"column:heartbeat_item_id;type:text;not null;index:idx_hbl_item"`
	Action          string    `gorm:"column:action;type:text;not null"`
	Reason          string    `gorm:"column:reason;type:text"`
	Result          string    `gorm:"column:result;type:text"`
	Timestamp       time.Time `gorm:"column:timestamp;autoCreateTime:false"`
}

func (HeartbeatLogModel) TableName() string { return "heartbeat_logs" }
