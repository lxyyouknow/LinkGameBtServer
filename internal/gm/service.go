// Package gm 提供统计后台内部账号登录和短期会话。
package gm

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"sync"
	"time"
)

var (
	ErrDisabled       = errors.New("统计后台登录未启用")
	ErrAuthentication = errors.New("统计后台账号或密码错误")
	ErrRateLimited    = errors.New("统计后台登录尝试过多")
)

const (
	maxLoginFailures = 5
	loginBlockTime   = 10 * time.Minute
)

type credential struct {
	account  []byte
	password []byte
}

type loginFailureEntry struct {
	count        int
	blockedUntil time.Time
}

type sessionEntry struct {
	expiresAt time.Time
}

// Service 仅把 GM 凭据保存在服务端环境变量中，明文不会进入数据库、日志或客户端包。
// 会话存放在进程内存中，服务重启后要求内部人员重新登录。
type Service struct {
	enabled     bool
	credentials []credential
	ttl         time.Duration
	now         func() time.Time
	mu          sync.Mutex
	sessions    map[string]sessionEntry
	failures    map[string]loginFailureEntry
}

func NewService(enabled bool, users map[string]string, ttl time.Duration) *Service {
	credentials := make([]credential, 0, len(users))
	for account, password := range users {
		credentials = append(credentials, credential{
			account:  []byte(account),
			password: []byte(password),
		})
	}
	return &Service{
		enabled:     enabled,
		credentials: credentials,
		ttl:         ttl,
		now:         time.Now,
		sessions:    make(map[string]sessionEntry),
		failures:    make(map[string]loginFailureEntry),
	}
}

func (service *Service) Login(account string, password string) (string, time.Time, error) {
	if !service.enabled {
		return "", time.Time{}, ErrDisabled
	}

	accountBytes := []byte(account)
	passwordBytes := []byte(password)
	authenticated := 0
	knownAccount := ""
	for _, candidate := range service.credentials {
		accountMatch := subtle.ConstantTimeSelect(
			constantTimeEqualInt(accountBytes, candidate.account),
			1,
			0,
		)
		passwordMatch := subtle.ConstantTimeSelect(
			constantTimeEqualInt(passwordBytes, candidate.password),
			1,
			0,
		)
		authenticated |= accountMatch & passwordMatch
		if accountMatch == 1 {
			knownAccount = string(candidate.account)
		}
	}

	now := service.now().UTC()
	service.mu.Lock()
	if knownAccount != "" {
		failure := service.failures[knownAccount]
		if failure.blockedUntil.After(now) {
			service.mu.Unlock()
			return "", time.Time{}, ErrRateLimited
		}
		if authenticated == 0 {
			failure.count++
			if failure.count >= maxLoginFailures {
				failure.blockedUntil = now.Add(loginBlockTime)
			}
			service.failures[knownAccount] = failure
			service.mu.Unlock()
			if failure.blockedUntil.After(now) {
				return "", time.Time{}, ErrRateLimited
			}
			return "", time.Time{}, ErrAuthentication
		}
		delete(service.failures, knownAccount)
	}
	service.mu.Unlock()

	if authenticated == 0 {
		return "", time.Time{}, ErrAuthentication
	}

	randomBytes := make([]byte, 32)
	if _, err := rand.Read(randomBytes); err != nil {
		return "", time.Time{}, err
	}
	token := base64.RawURLEncoding.EncodeToString(randomBytes)
	expiresAt := now.Add(service.ttl)

	service.mu.Lock()
	service.removeExpiredLocked(now)
	service.sessions[token] = sessionEntry{expiresAt: expiresAt}
	service.mu.Unlock()
	return token, expiresAt, nil
}

func (service *Service) Authenticate(token string) bool {
	if !service.enabled || len(token) < 32 || len(token) > 128 {
		return false
	}
	now := service.now().UTC()
	service.mu.Lock()
	defer service.mu.Unlock()
	entry, exists := service.sessions[token]
	if !exists || !entry.expiresAt.After(now) {
		delete(service.sessions, token)
		return false
	}
	return true
}

func (service *Service) Logout(token string) {
	service.mu.Lock()
	delete(service.sessions, token)
	service.mu.Unlock()
}

func (service *Service) removeExpiredLocked(now time.Time) {
	for token, entry := range service.sessions {
		if !entry.expiresAt.After(now) {
			delete(service.sessions, token)
		}
	}
}

func constantTimeEqual(left []byte, right []byte) bool {
	return constantTimeEqualInt(left, right) == 1
}

func constantTimeEqualInt(left []byte, right []byte) int {
	if len(left) != len(right) {
		// 仍执行一次比较，避免长度错误路径完全跳过恒定时间操作。
		padded := make([]byte, len(right))
		copy(padded, left)
		_ = subtle.ConstantTimeCompare(padded, right)
		return 0
	}
	return subtle.ConstantTimeCompare(left, right)
}
