// Package auth 负责游客登录、会话 Token 生成和身份认证。
package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"
)

var (
	// ErrInvalidInstallationID 表示客户端安装标识格式不合法。
	ErrInvalidInstallationID = errors.New("installation_id 格式不合法")
	// ErrInvalidTestAccount 表示免密码测试账号格式不合法。
	ErrInvalidTestAccount = errors.New("测试账号格式不合法")
	// ErrInvalidVerifiedIdentity 表示平台服务传入的已验证身份格式不合法。
	ErrInvalidVerifiedIdentity = errors.New("已验证平台身份格式不合法")
	// ErrUnauthenticated 表示会话无效、过期或已撤销。
	ErrUnauthenticated = errors.New("会话无效")
	// ErrAccountDisabled 表示玩家账号已被禁用。
	ErrAccountDisabled = errors.New("账号已禁用")
)

var installationIDPattern = regexp.MustCompile(
	`^[A-Za-z0-9][A-Za-z0-9._:-]{15,127}$`,
)

var testAccountPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{2,31}$`)

var verifiedProviderPattern = regexp.MustCompile(`^[a-z][a-z0-9_]{1,31}$`)

// Player 是服务端验证后的玩家身份。ID 仅用于服务端数据库查询，不能返回客户端。
type Player struct {
	ID       uint64
	PublicID string
}

// IdentityLoginParams 是通过一个已校验身份创建或刷新会话所需的数据。
type IdentityLoginParams struct {
	Provider    string
	ProviderUID string
	PublicID    string
	TokenHash   [sha256.Size]byte
	ExpiresAt   time.Time
	Now         time.Time
}

// LoginResult 是登录成功后返回给 HTTP 层的数据。
type LoginResult struct {
	Player    Player
	Token     string
	ExpiresAt time.Time
}

// TestAccountLoginResult 包含规范化后的测试账号和登录会话。
type TestAccountLoginResult struct {
	Account string
	LoginResult
}

// Store 定义玩家身份与会话需要的持久化能力。
type Store interface {
	LoginIdentity(
		ctx context.Context,
		params IdentityLoginParams,
	) (Player, error)
	AuthenticateSession(
		ctx context.Context,
		tokenHash [sha256.Size]byte,
		now time.Time,
	) (Player, error)
}

// Service 提供游客、测试账号登录和会话认证业务。
type Service struct {
	store      Store
	sessionTTL time.Duration
	random     io.Reader
	now        func() time.Time
}

// NewService 创建认证服务。
func NewService(store Store, sessionTTL time.Duration) *Service {
	return &Service{
		store:      store,
		sessionTTL: sessionTTL,
		random:     rand.Reader,
		now:        time.Now,
	}
}

// LoginGuest 查找或创建游客玩家，并签发新的随机会话 Token。
func (service *Service) LoginGuest(
	ctx context.Context,
	installationID string,
) (LoginResult, error) {
	if !installationIDPattern.MatchString(installationID) {
		return LoginResult{}, ErrInvalidInstallationID
	}

	return service.loginIdentity(ctx, "guest", installationID)
}

// LoginTestAccount 使用免密码测试账号登录。
// 账号只用于 development/test 环境，HTTP 层负责在其他环境禁用入口。
func (service *Service) LoginTestAccount(
	ctx context.Context,
	account string,
) (TestAccountLoginResult, error) {
	normalizedAccount, err := NormalizeTestAccount(account)
	if err != nil {
		return TestAccountLoginResult{}, err
	}

	result, err := service.loginIdentity(
		ctx,
		"test_account",
		normalizedAccount,
	)
	if err != nil {
		return TestAccountLoginResult{}, err
	}
	return TestAccountLoginResult{
		Account:     normalizedAccount,
		LoginResult: result,
	}, nil
}

// NormalizeTestAccount 统一测试账号格式，供登录和受控管理工具复用。
func NormalizeTestAccount(account string) (string, error) {
	normalizedAccount := strings.ToLower(strings.TrimSpace(account))
	if !testAccountPattern.MatchString(normalizedAccount) {
		return "", ErrInvalidTestAccount
	}
	return normalizedAccount, nil
}

// LoginVerifiedIdentity 为已由可信服务端流程验证的平台身份签发本站会话。
// providerUID 必须是平台返回的原始稳定标识，不能使用客户端自行声明的用户 ID。
func (service *Service) LoginVerifiedIdentity(
	ctx context.Context,
	provider string,
	providerUID string,
) (LoginResult, error) {
	if !verifiedProviderPattern.MatchString(provider) ||
		providerUID == "" ||
		len(providerUID) > 191 ||
		strings.TrimSpace(providerUID) != providerUID {
		return LoginResult{}, ErrInvalidVerifiedIdentity
	}

	return service.loginIdentity(ctx, provider, providerUID)
}

func (service *Service) loginIdentity(
	ctx context.Context,
	provider string,
	providerUID string,
) (LoginResult, error) {
	publicID, err := service.newPublicID()
	if err != nil {
		return LoginResult{}, err
	}
	token, err := service.newToken()
	if err != nil {
		return LoginResult{}, err
	}

	now := service.now().UTC()
	expiresAt := now.Add(service.sessionTTL)
	tokenHash := sha256.Sum256([]byte(token))
	player, err := service.store.LoginIdentity(ctx, IdentityLoginParams{
		Provider:    provider,
		ProviderUID: providerUID,
		PublicID:    publicID,
		TokenHash:   tokenHash,
		ExpiresAt:   expiresAt,
		Now:         now,
	})
	if err != nil {
		return LoginResult{}, err
	}

	return LoginResult{
		Player:    player,
		Token:     token,
		ExpiresAt: expiresAt,
	}, nil
}

// AuthenticateToken 验证 Bearer Token，并返回可信玩家身份。
func (service *Service) AuthenticateToken(
	ctx context.Context,
	token string,
) (Player, error) {
	// 当前生成的 Token 长度为 43。限制输入长度可避免异常超长 Header。
	if len(token) < 32 || len(token) > 256 {
		return Player{}, ErrUnauthenticated
	}

	tokenHash := sha256.Sum256([]byte(token))
	player, err := service.store.AuthenticateSession(
		ctx,
		tokenHash,
		service.now().UTC(),
	)
	if err != nil {
		return Player{}, err
	}
	return player, nil
}

func (service *Service) newToken() (string, error) {
	randomBytes := make([]byte, 32)
	if _, err := io.ReadFull(service.random, randomBytes); err != nil {
		return "", fmt.Errorf("生成会话 Token 失败: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(randomBytes), nil
}

func (service *Service) newPublicID() (string, error) {
	randomBytes := make([]byte, 16)
	if _, err := io.ReadFull(service.random, randomBytes); err != nil {
		return "", fmt.Errorf("生成玩家公开 ID 失败: %w", err)
	}

	// RFC 4122 UUID v4：设置版本位与 variant 位。
	randomBytes[6] = (randomBytes[6] & 0x0f) | 0x40
	randomBytes[8] = (randomBytes[8] & 0x3f) | 0x80

	encoded := hex.EncodeToString(randomBytes)
	return encoded[0:8] + "-" +
		encoded[8:12] + "-" +
		encoded[12:16] + "-" +
		encoded[16:20] + "-" +
		encoded[20:32], nil
}
