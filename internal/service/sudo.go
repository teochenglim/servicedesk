package service

import (
	"encoding/json"
	"errors"

	"servicedesk/internal/auth"
	"servicedesk/internal/models"
	"servicedesk/internal/repo"
)

var ErrNotInSudoSession = errors.New("not in a sudo session")

// SudoService implements Sudo-as (DESIGN/02 §2.5): a SystemAdmin acting as
// another user performs real actions attributed to the target's identity,
// not a read-only "view as" preview. This is deliberately the only path back
// to Manager's queue/routing actions for SystemAdmin (§2.1.1) - it mints a
// JWT carrying the target's identity so every existing RBAC/visibility check
// downstream works completely unmodified.
type SudoService struct {
	users      *repo.UserRepo
	orgMembers *repo.OrgMembershipRepo
	events     *repo.EventLogRepo
	authMgr    *auth.Manager
}

func NewSudoService(users *repo.UserRepo, orgMembers *repo.OrgMembershipRepo, events *repo.EventLogRepo, authMgr *auth.Manager) *SudoService {
	return &SudoService{users: users, orgMembers: orgMembers, events: events, authMgr: authMgr}
}

// Start mints a sudo-flavored session token for targetUserID. Capability
// checked here too, not just at the route (ARCHITECTURE.md's defense-in-depth
// rule). A Customer target's OrgID is resolved to their first org membership -
// sudo is for staff coverage, not customer impersonation, so "first org" is a
// reasonable default rather than a picker.
func (s *SudoService) Start(admin *auth.Claims, targetUserID int64) (token string, target *models.User, err error) {
	if !admin.Role.Can(models.CapSudo) {
		return "", nil, ErrForbidden
	}
	target, err = s.users.GetByID(targetUserID)
	if err != nil {
		return "", nil, err
	}
	if target.ID == admin.UserID {
		return "", nil, errors.New("cannot sudo into your own account")
	}

	var orgID int64
	if target.Role == models.RoleCustomer {
		orgs, oerr := s.orgMembers.ListOrgsForUser(target.ID)
		if oerr == nil && len(orgs) > 0 {
			orgID = orgs[0].ID
		}
	}

	token, err = s.authMgr.IssueSudoToken(*target, orgID, admin.UserID, admin.Username)
	if err != nil {
		return "", nil, err
	}

	details, _ := json.Marshal(map[string]any{"admin_id": admin.UserID, "admin_username": admin.Username})
	if aerr := s.events.Append(&models.EventLog{ActorID: &target.ID, Event: "sudo_started", Details: string(details), SudoByID: &admin.UserID}); aerr != nil {
		return "", nil, aerr
	}
	return token, target, nil
}

// Stop records the end of a sudo session, attributed the same way as every
// other sudo-session event: under the target's (claims') identity, with the
// real admin's ID as SudoByID.
func (s *SudoService) Stop(claims *auth.Claims) error {
	if claims.SudoByID == nil {
		return ErrNotInSudoSession
	}
	return s.events.Append(&models.EventLog{ActorID: &claims.UserID, Event: "sudo_stopped", SudoByID: claims.SudoByID})
}
