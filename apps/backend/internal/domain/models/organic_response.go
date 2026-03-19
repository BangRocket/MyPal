// Copyright (c) MyPal contributors. See LICENSE for details.

package models

import "time"

// OrganicResponseConfigModel stores per-channel organic response settings.
type OrganicResponseConfigModel struct {
	ID                 string    `gorm:"primaryKey;column:id;type:text" json:"id"`
	ChannelID          string    `gorm:"column:channel_id;type:text;not null;uniqueIndex" json:"channel_id"`
	Enabled            bool      `gorm:"column:enabled;default:false" json:"enabled"`
	CooldownSeconds    int       `gorm:"column:cooldown_seconds;default:300" json:"cooldown_seconds"`
	RelevanceThreshold float64   `gorm:"column:relevance_threshold;default:0.7" json:"relevance_threshold"`
	MaxDailyOrganic    int       `gorm:"column:max_daily_organic;default:20" json:"max_daily_organic"`
	AllowReactions     bool      `gorm:"column:allow_reactions;default:false" json:"allow_reactions"`
	ThreadPolicy       string    `gorm:"column:thread_policy;type:text;default:'joined_only'" json:"thread_policy"`
	QuietHoursStart    string    `gorm:"column:quiet_hours_start;type:text" json:"quiet_hours_start"`
	QuietHoursEnd      string    `gorm:"column:quiet_hours_end;type:text" json:"quiet_hours_end"`
	CreatedAt          time.Time `gorm:"column:created_at;autoCreateTime:false" json:"created_at"`
	UpdatedAt          time.Time `gorm:"column:updated_at;autoUpdateTime:false" json:"updated_at"`
}

func (OrganicResponseConfigModel) TableName() string { return "organic_response_configs" }
