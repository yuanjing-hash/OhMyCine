package services

import (
	"sort"

	"github.com/yuanjing-hash/ohmycine/server/internal/authz"
	"github.com/yuanjing-hash/ohmycine/server/internal/models"
)

// Actor is the authenticated authorization context used by policy-aware services.
type Actor struct {
	User        models.User
	RoleCodes   []string
	Permissions map[string]struct{}
}

func (a Actor) Can(code string) bool {
	_, ok := a.Permissions[code]
	return ok
}

func (a Actor) IsSystemAdmin() bool { return a.Can(authz.PermissionSystemAdmin) }

func (a Actor) SortedPermissions() []string {
	codes := make([]string, 0, len(a.Permissions))
	for code := range a.Permissions {
		codes = append(codes, code)
	}
	sort.Strings(codes)
	return codes
}

type RequestContext struct {
	RequestID string
	IPHint    string
}

type CurrentUser struct {
	ID          uint     `json:"id"`
	Username    string   `json:"username"`
	DisplayName string   `json:"display_name"`
	Status      string   `json:"status"`
	IsOwner     bool     `json:"is_owner"`
	Roles       []string `json:"roles"`
	Permissions []string `json:"permissions"`
}

func CurrentUserFromActor(actor Actor) CurrentUser {
	roles := append([]string(nil), actor.RoleCodes...)
	sort.Strings(roles)
	return CurrentUser{
		ID: actor.User.ID, Username: actor.User.Username, DisplayName: actor.User.DisplayName,
		Status: actor.User.Status, IsOwner: actor.User.IsOwner, Roles: roles,
		Permissions: actor.SortedPermissions(),
	}
}
