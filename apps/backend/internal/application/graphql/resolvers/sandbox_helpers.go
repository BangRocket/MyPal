package resolvers

import (
	"github.com/BangRocket/MyPal/apps/backend/internal/application/graphql/generated"
	"github.com/BangRocket/MyPal/apps/backend/internal/domain/ports"
)

func sandboxInstanceToGenerated(inst *ports.SandboxInstance) *generated.SandboxInstance {
	return &generated.SandboxInstance{
		ID:         inst.ID,
		Image:      inst.Image,
		Status:     inst.Status,
		UserID:     inst.UserID,
		MemLimit:   int(inst.MemLimit),
		CPULimit:   inst.CPULimit,
		NetPolicy:  inst.NetPolicy,
		Persistent: inst.Persistent,
		CreatedAt:  inst.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
	}
}
