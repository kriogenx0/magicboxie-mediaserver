package auth

import (
	"crypto/rand"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"
)

const tokenTTL = 30 * 24 * time.Hour

type Manager struct {
	signingKey []byte
}

// NewManager loads the JWT signing key from <dataDir>/jwt.key, generating one
// on first boot. Deleting this file and restarting invalidates every
// outstanding token at once.
func NewManager(dataDir string) (*Manager, error) {
	keyPath := filepath.Join(dataDir, "jwt.key")

	key, err := os.ReadFile(keyPath)
	if err == nil {
		return &Manager{signingKey: key}, nil
	}
	if !os.IsNotExist(err) {
		return nil, fmt.Errorf("reading jwt key at %q: %w", keyPath, err)
	}

	key = make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("generating jwt key: %w", err)
	}
	if err := os.WriteFile(keyPath, key, 0o600); err != nil {
		return nil, fmt.Errorf("writing jwt key at %q: %w", keyPath, err)
	}

	return &Manager{signingKey: key}, nil
}

// HashPassword bcrypt-hashes a plaintext password for storage in config.
func HashPassword(password string) (string, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", err
	}
	return string(hash), nil
}

// CheckPassword compares a plaintext password against a bcrypt hash.
func CheckPassword(hash, password string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}

// lanAudience marks a token issued without a password because the request
// came from the home network. Such a token is only accepted on LAN requests,
// so one copied off a home device can't be used through the public address.
const lanAudience = "lan"

// IssueToken mints a new JWT valid for tokenTTL, accepted on any network.
func (m *Manager) IssueToken() (token string, expiresAt time.Time, err error) {
	return m.issue(nil)
}

// IssueLANToken mints a JWT that only works on requests from the home network.
func (m *Manager) IssueLANToken() (token string, expiresAt time.Time, err error) {
	return m.issue(jwt.ClaimStrings{lanAudience})
}

func (m *Manager) issue(audience jwt.ClaimStrings) (token string, expiresAt time.Time, err error) {
	expiresAt = time.Now().Add(tokenTTL)
	claims := jwt.RegisteredClaims{
		Subject:   "magicboxie",
		Audience:  audience,
		IssuedAt:  jwt.NewNumericDate(time.Now()),
		ExpiresAt: jwt.NewNumericDate(expiresAt),
	}
	t := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := t.SignedString(m.signingKey)
	if err != nil {
		return "", time.Time{}, err
	}
	return signed, expiresAt, nil
}

// VerifyToken validates a token's signature and expiry, and reports whether
// it is LAN-only (see IssueLANToken).
func (m *Manager) VerifyToken(tokenString string) (lanOnly bool, err error) {
	var claims jwt.RegisteredClaims
	_, err = jwt.ParseWithClaims(tokenString, &claims, func(t *jwt.Token) (interface{}, error) {
		return m.signingKey, nil
	}, jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}))
	if err != nil {
		return false, err
	}
	for _, aud := range claims.Audience {
		if aud == lanAudience {
			return true, nil
		}
	}
	return false, nil
}
