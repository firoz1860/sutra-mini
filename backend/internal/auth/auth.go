package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"strconv"
	"strings"
	"time"
)

var (
	ErrNotConfigured      = errors.New("operator auth is not configured")
	ErrInvalidCredentials = errors.New("invalid credentials")
)

// Authenticator issues and verifies stateless, HMAC-signed operator session
// tokens. It holds no database state: a token is valid iff its signature
// matches the configured secret and its embedded expiry is in the future.
type Authenticator struct {
	password string
	secret   []byte
	ttl      time.Duration
}

func New(password, secret string, ttl time.Duration) *Authenticator {
	if ttl <= 0 {
		ttl = 12 * time.Hour
	}
	return &Authenticator{password: password, secret: []byte(secret), ttl: ttl}
}

// Configured reports whether both an operator password and a signing secret
// are set. When false, Login is unavailable and every token fails to verify,
// so management endpoints stay locked by default.
func (a *Authenticator) Configured() bool {
	return a.password != "" && len(a.secret) > 0
}

func (a *Authenticator) TTL() time.Duration { return a.ttl }

func (a *Authenticator) Login(password string) (string, time.Time, error) {
	if !a.Configured() {
		return "", time.Time{}, ErrNotConfigured
	}
	if subtle.ConstantTimeCompare([]byte(password), []byte(a.password)) != 1 {
		return "", time.Time{}, ErrInvalidCredentials
	}
	exp := time.Now().Add(a.ttl)
	return a.mint(exp), exp, nil
}

func (a *Authenticator) mint(exp time.Time) string {
	payload := "v1." + strconv.FormatInt(exp.Unix(), 10)
	return payload + "." + a.sign(payload)
}

func (a *Authenticator) sign(payload string) string {
	mac := hmac.New(sha256.New, a.secret)
	mac.Write([]byte(payload))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func (a *Authenticator) Verify(token string) bool {
	if !a.Configured() || token == "" {
		return false
	}
	i := strings.LastIndexByte(token, '.')
	if i < 0 {
		return false
	}
	payload, sig := token[:i], token[i+1:]
	if !strings.HasPrefix(payload, "v1.") {
		return false
	}
	if subtle.ConstantTimeCompare([]byte(sig), []byte(a.sign(payload))) != 1 {
		return false
	}
	expUnix, err := strconv.ParseInt(payload[len("v1."):], 10, 64)
	if err != nil {
		return false
	}
	return time.Now().Unix() < expUnix
}
