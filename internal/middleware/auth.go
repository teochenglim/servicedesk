package middleware

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"servicedesk/internal/auth"
	"servicedesk/internal/models"
)

type ctxKey string

const claimsKey ctxKey = "claims"

// APITokenLookup resolves an API token's public ID to its owning user (see
// repo.UserRepo.GetByAPITokenID), for the long-lived Agent API-token auth
// path (DESIGN/08 §8.1). Kept as a function type rather than importing repo
// directly, so middleware doesn't depend on the repo/db layer.
type APITokenLookup func(tokenID string) (*models.User, error)

// RequireAuth extracts a bearer credential from the Authorization header or
// the sd_token cookie (so plain HTMX navigations without JS-set headers
// still work), and accepts either a human JWT session or a long-lived API
// token (currently only issued to RoleAgent - auth.IssueAPIToken).
func RequireAuth(mgr *auth.Manager, lookupAPIToken APITokenLookup) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token := bearerToken(r)
			if token == "" {
				if c, err := r.Cookie("sd_token"); err == nil {
					token = c.Value
				}
			}
			if token == "" {
				unauthorized(w, r)
				return
			}

			var claims *auth.Claims
			if tokenID, secret, ok := auth.ParseAPIToken(token); ok {
				c, err := resolveAPIToken(lookupAPIToken, tokenID, secret)
				if err != nil {
					unauthorized(w, r)
					return
				}
				claims = c
			} else {
				c, err := mgr.ParseToken(token)
				if err != nil {
					unauthorized(w, r)
					return
				}
				claims = c
			}

			ctx := context.WithValue(r.Context(), claimsKey, claims)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func resolveAPIToken(lookup APITokenLookup, tokenID, secret string) (*auth.Claims, error) {
	if lookup == nil {
		return nil, errors.New("api token auth not configured")
	}
	u, err := lookup(tokenID)
	if err != nil {
		return nil, err
	}
	if u.APITokenHash == nil || !auth.VerifyAPIToken(secret, *u.APITokenHash) {
		return nil, errors.New("invalid api token")
	}
	// OrgID is 0 (unscoped) - API tokens are only issued to internal-staff
	// roles (RoleAgent), which are never org-scoped (DESIGN/02 §2.3).
	return &auth.Claims{UserID: u.ID, Username: u.Username, Role: u.Role}, nil
}

func unauthorized(w http.ResponseWriter, r *http.Request) {
	if strings.Contains(r.Header.Get("Accept"), "application/json") {
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
		return
	}
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

func bearerToken(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if strings.HasPrefix(h, "Bearer ") {
		return strings.TrimPrefix(h, "Bearer ")
	}
	return ""
}

// RequireRole gates a handler to users whose role rank is >= min.
func RequireRole(min models.Role) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			claims := ClaimsFrom(r.Context())
			if claims == nil || !claims.Role.AtLeast(min) {
				http.Error(w, "forbidden", http.StatusForbidden)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// RequireCapability gates a handler to users whose role exactly holds cap
// (models.Role.Can), not by rank. This is what lets a capability like queue
// ownership stay exclusive to Manager instead of being inherited by anyone
// who outranks it - see DESIGN/02 §2.1.1. Once Sudo-as (§2.5) lands, this is
// the check that must consult the sudo-target's effective role rather than
// the real actor's, which is why it's kept separate from RequireRole.
func RequireCapability(cap models.Capability) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			claims := ClaimsFrom(r.Context())
			if claims == nil || !claims.Role.Can(cap) {
				http.Error(w, "forbidden", http.StatusForbidden)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func ClaimsFrom(ctx context.Context) *auth.Claims {
	c, _ := ctx.Value(claimsKey).(*auth.Claims)
	return c
}
