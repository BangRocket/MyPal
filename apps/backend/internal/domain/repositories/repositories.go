// Copyright (c) OpenLobster contributors. See LICENSE for details.

// Package repositories provides data access implementations. Each subpackage
// holds one repository with its service and test.
package repositories

import (
	channelPkg "github.com/BangRocket/MyPal/apps/backend/internal/domain/repositories/channel"
	conversationPkg "github.com/BangRocket/MyPal/apps/backend/internal/domain/repositories/conversation"
	groupPkg "github.com/BangRocket/MyPal/apps/backend/internal/domain/repositories/group"
	mcpServerPkg "github.com/BangRocket/MyPal/apps/backend/internal/domain/repositories/mcp_server"
	messagePkg "github.com/BangRocket/MyPal/apps/backend/internal/domain/repositories/message"
	pairingPkg "github.com/BangRocket/MyPal/apps/backend/internal/domain/repositories/pairing"
	sessionPkg "github.com/BangRocket/MyPal/apps/backend/internal/domain/repositories/session"
	taskPkg "github.com/BangRocket/MyPal/apps/backend/internal/domain/repositories/task"
	toolPermissionPkg "github.com/BangRocket/MyPal/apps/backend/internal/domain/repositories/tool_permission"
	userPkg "github.com/BangRocket/MyPal/apps/backend/internal/domain/repositories/user"
	userChannelPkg "github.com/BangRocket/MyPal/apps/backend/internal/domain/repositories/user_channel"
)

// Re-exports for backward compatibility.
var (
	NewChannelRepository          = channelPkg.NewChannelRepository
	NewConversationRepository     = conversationPkg.NewConversationRepository
	NewGroupRepository            = groupPkg.NewGroupRepository
	NewMessageRepository          = messagePkg.NewMessageRepository
	NewPairingRepository          = pairingPkg.NewPairingRepository
	NewSessionRepository          = sessionPkg.NewSessionRepository
	NewTaskRepository             = taskPkg.NewTaskRepository
	NewUserRepository             = userPkg.NewUserRepository
	NewUserChannelRepository      = userChannelPkg.NewUserChannelRepository
	NewToolPermissionRepository   = toolPermissionPkg.NewToolPermissionRepository
	NewMCPServerRepository        = mcpServerPkg.NewMCPServerRepository
	NewDashboardMessageRepository = messagePkg.NewDashboardMessageRepository
	NewDashboardTaskRepository    = taskPkg.NewDashboardTaskRepository
)

// Type aliases for types defined in subpackages.
type (
	ConversationRow              = conversationPkg.ConversationRow
	ConversationRepository       = conversationPkg.ConversationRepository
	TaskRepository               = taskPkg.TaskRepository
	DashboardTaskRepository      = taskPkg.DashboardTaskRepository
	SessionRepository            = sessionPkg.SessionRepository
	DashboardMessageRepository   = messagePkg.DashboardMessageRepository
	ToolPermissionRecord         = toolPermissionPkg.ToolPermissionRecord
	ToolPermissionRepositoryPort = toolPermissionPkg.ToolPermissionRepositoryPort
	MCPServerRecord              = mcpServerPkg.MCPServerRecord
	MCPServerRepositoryPort      = mcpServerPkg.MCPServerRepositoryPort
)
