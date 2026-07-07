package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"

	"servicedesk/internal/models"
)

var ErrInvalidCredentials = errors.New("invalid credentials")

// apiTokenPrefix marks a bearer token as a long-lived API token (currently
// only issued to RoleAgent, DESIGN/08 §8.1) rather than a human JWT session.
// This is a narrow, swappable auth path, not a general framework - a future
// OIDC/external-IdP login for Agent would replace this branch, not extend it
// (same "documented extension point, not fully wired" shape as LDAP_ENABLED).
const apiTokenPrefix = "sdat_"

// IssueAPIToken generates a new token, returning the full string to show the
// caller exactly once (format: "sdat_<tokenID>.<secret>"), plus the tokenID
// and hashed secret to persist via repo.UserRepo.SetAPIToken. The plaintext
// secret is never stored - only its sha256 hash, checked with a
// constant-time comparison in VerifyAPIToken since the secret is already
// high-entropy random data, not a low-entropy password needing bcrypt's cost factor.
func IssueAPIToken() (token, tokenID, tokenHash string, err error) {
	idBytes := make([]byte, 8)
	if _, err = rand.Read(idBytes); err != nil {
		return "", "", "", err
	}
	secretBytes := make([]byte, 24)
	if _, err = rand.Read(secretBytes); err != nil {
		return "", "", "", err
	}
	tokenID = hex.EncodeToString(idBytes)
	secret := hex.EncodeToString(secretBytes)
	tokenHash = hashAPITokenSecret(secret)
	token = apiTokenPrefix + tokenID + "." + secret
	return token, tokenID, tokenHash, nil
}

// ParseAPIToken splits a bearer token into its public tokenID and secret if
// it looks like an API token (see IsAPIToken); ok is false for a plain JWT.
func ParseAPIToken(token string) (tokenID, secret string, ok bool) {
	if !strings.HasPrefix(token, apiTokenPrefix) {
		return "", "", false
	}
	tokenID, secret, found := strings.Cut(strings.TrimPrefix(token, apiTokenPrefix), ".")
	if !found {
		return "", "", false
	}
	return tokenID, secret, true
}

// IsAPIToken reports whether a bearer token looks like an API token (vs. a JWT).
func IsAPIToken(token string) bool { return strings.HasPrefix(token, apiTokenPrefix) }

// VerifyAPIToken checks a candidate secret against the stored hash.
func VerifyAPIToken(secret, storedHash string) bool {
	return subtle.ConstantTimeCompare([]byte(hashAPITokenSecret(secret)), []byte(storedHash)) == 1
}

func hashAPITokenSecret(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:])
}

type Claims struct {
	UserID   int64       `json:"uid"`
	Username string      `json:"username"`
	Role     models.Role `json:"role"`
	// OrgID is the organization this session is scoped to (multi-tenant
	// Customer visibility); 0 for internal staff, who see across all orgs.
	OrgID int64 `json:"org_id"`
	jwt.RegisteredClaims
}

type Manager struct {
	secret []byte
	issuer string
	ttl    time.Duration
}

func NewManager(secret, issuer string) *Manager {
	return &Manager{secret: []byte(secret), issuer: issuer, ttl: 12 * time.Hour}
}

func (m *Manager) IssueToken(u models.User, orgID int64) (string, error) {
	now := time.Now()
	claims := Claims{
		UserID:   u.ID,
		Username: u.Username,
		Role:     u.Role,
		OrgID:    orgID,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    m.issuer,
			Subject:   u.Username,
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(m.ttl)),
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(m.secret)
}

func (m *Manager) ParseToken(tokenStr string) (*Claims, error) {
	claims := &Claims{}
	token, err := jwt.ParseWithClaims(tokenStr, claims, func(t *jwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
		return m.secret, nil
	})
	if err != nil || !token.Valid {
		return nil, fmt.Errorf("invalid token: %w", err)
	}
	return claims, nil
}

func HashPassword(plain string) (string, error) {
	b, err := bcrypt.GenerateFromPassword([]byte(plain), bcrypt.DefaultCost)
	return string(b), err
}

func CheckPassword(hash, plain string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(plain)) == nil
}
