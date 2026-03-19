// Copyright (c) MyPal contributors. See LICENSE for details.

package models

import "time"

// PersonalityModel defines a personality configuration.
type PersonalityModel struct {
	ID          string    `gorm:"primaryKey;column:id" json:"id"`
	Name        string    `gorm:"column:name;not null" json:"name"`
	BasePrompt  string    `gorm:"column:base_prompt" json:"base_prompt"`
	Traits      string    `gorm:"column:traits" json:"traits"`           // JSON array of strings
	Tone        string    `gorm:"column:tone" json:"tone"`
	Boundaries  string    `gorm:"column:boundaries" json:"boundaries"`   // JSON array of strings
	Quirks      string    `gorm:"column:quirks" json:"quirks"`           // JSON array of strings
	Adaptations string    `gorm:"column:adaptations" json:"adaptations"` // JSON object: channel_type -> adaptation text
	IsDefault   bool      `gorm:"column:is_default;default:false" json:"is_default"`
	CreatedAt   time.Time `gorm:"column:created_at;autoCreateTime:false" json:"created_at"`
	UpdatedAt   time.Time `gorm:"column:updated_at;autoUpdateTime:false" json:"updated_at"`
}

func (PersonalityModel) TableName() string { return "personalities" }

// UserPersonaRelationshipModel tracks per-user familiarity with a personality.
type UserPersonaRelationshipModel struct {
	ID               string    `gorm:"primaryKey;column:id" json:"id"`
	UserID           string    `gorm:"column:user_id;not null;index:idx_upr_user" json:"user_id"`
	PersonalityID    string    `gorm:"column:personality_id;not null;index:idx_upr_personality" json:"personality_id"`
	Familiarity      float64   `gorm:"column:familiarity;default:0.0" json:"familiarity"`       // 0.0-1.0, grows over time
	Preferences      string    `gorm:"column:preferences" json:"preferences"`                    // JSON object of learned prefs
	InteractionCount int64     `gorm:"column:interaction_count;default:0" json:"interaction_count"`
	LastInteraction  time.Time `gorm:"column:last_interaction" json:"last_interaction"`
	CreatedAt        time.Time `gorm:"column:created_at;autoCreateTime:false" json:"created_at"`
	UpdatedAt        time.Time `gorm:"column:updated_at;autoUpdateTime:false" json:"updated_at"`
}

func (UserPersonaRelationshipModel) TableName() string { return "user_persona_relationships" }
