package auth

import (
	"crypto/rand"
	"encoding/hex"
	"log/slog"

	"servicedesk/internal/config"
	"servicedesk/internal/models"
	"servicedesk/internal/repo"
)

// randomToken produces an unguessable string used as the "system" user's
// password hash input; that account can never log in interactively.
func randomToken() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// SystemActorID is the reserved user automation-authored notes/events are
// attributed to. It relies on being the very first row inserted into a fresh
// database, which Bootstrap guarantees by running before static users.
const SystemActorID int64 = 1

// Bootstrap seeds the reserved system actor and any GOATFLOW_STATIC_USERS
// demo accounts (DESIGN.md 3.9), and a first-run admin login if none exists yet.
func Bootstrap(users *repo.UserRepo, cfg config.Config, log *slog.Logger) error {
	if _, err := users.GetByID(SystemActorID); err != nil {
		hash, err := HashPassword(randomToken())
		if err != nil {
			return err
		}
		if err := users.Create(&models.User{
			Username: "system", Email: "", PasswordHash: hash, Role: models.RoleSystemAdmin, Source: "system",
		}); err != nil {
			return err
		}
	}

	entries := cfg.StaticUserEntries()
	for _, e := range entries {
		username, password, role := e[0], e[1], e[2]
		hash, err := HashPassword(password)
		if err != nil {
			return err
		}
		if err := users.UpsertStatic(username, hash, models.Role(role)); err != nil {
			return err
		}
		log.Info("auth: static user configured", "username", username, "role", role)
	}

	if len(entries) == 0 {
		if _, err := users.GetByUsername("admin"); err != nil {
			hash, err := HashPassword("admin123")
			if err != nil {
				return err
			}
			if err := users.Create(&models.User{
				Username: "admin", Email: "admin@example.com", PasswordHash: hash, Role: models.RoleSystemAdmin, Source: "db",
			}); err != nil {
				return err
			}
			log.Warn("auth: no GOATFLOW_STATIC_USERS set, created default admin/admin123 - change this password")
		}
	}
	return nil
}
