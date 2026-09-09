// Package platform 负责把第三方平台的一次性登录凭证换成本站会话。
package platform

import (
	"context"
	"errors"
	"strings"

	"linkgame-server/internal/auth"
)

var (
	// ErrUnsupportedProvider 表示当前服务没有接入请求的平台。
	ErrUnsupportedProvider = errors.New("不支持的登录平台")
	// ErrInvalidCode 表示客户端没有提供格式合理的一次性登录凭证。
	ErrInvalidCode = errors.New("平台登录凭证格式不合法")
	// ErrCodeInvalid 表示平台拒绝了已过期、已使用或错误的登录凭证。
	ErrCodeInvalid = errors.New("平台登录凭证无效")
	// ErrAuthUnavailable 表示平台认证服务暂时不可用。
	ErrAuthUnavailable = errors.New("平台认证服务暂时不可用")
	// ErrAuthConfiguration 表示服务端平台密钥配置不正确。
	ErrAuthConfiguration = errors.New("平台认证配置错误")
	// ErrInvalidResponse 表示平台返回了无法安全使用的响应。
	ErrInvalidResponse = errors.New("平台认证响应不合法")
	// ErrScopeDenied 表示显式授权结果不包含业务所需权限。
	ErrScopeDenied = errors.New("平台授权范围不足")
	// ErrProfileUnauthorized 表示资料权限已失效或被玩家撤回。
	ErrProfileUnauthorized = errors.New("平台资料授权已失效")
)

// VerifiedIdentity 是平台服务端确认后的稳定身份。
type VerifiedIdentity struct {
	Provider    string
	ProviderUID string
}

// AuthorizedProfile 是显式授权后从平台服务端读取的最小公开资料。
// access_token、refresh_token 和原始响应禁止离开平台 Adapter。
type AuthorizedProfile struct {
	Provider    string
	ProviderUID string
	DisplayName string
	AvatarURL   string
}

// Verifier 验证平台的一次性登录凭证。实现不得把凭证或平台 Token 写入日志。
type Verifier interface {
	VerifyLoginCode(ctx context.Context, code string) (VerifiedIdentity, error)
}

// IdentitySessionIssuer 为已验证的平台身份签发本站会话。
type IdentitySessionIssuer interface {
	LoginVerifiedIdentity(
		ctx context.Context,
		provider string,
		providerUID string,
	) (auth.LoginResult, error)
}

// LoginResult 是平台名和本站会话的组合，不包含平台 OpenID 或 OAuth Token。
type LoginResult struct {
	Provider string
	auth.LoginResult
}

// LoginService 编排平台凭证验证和本站会话签发。
type LoginService struct {
	verifiers map[string]Verifier
	issuer    IdentitySessionIssuer
}

// NewLoginService 创建平台登录服务。verifiers 的键使用规范化小写平台名。
func NewLoginService(
	verifiers map[string]Verifier,
	issuer IdentitySessionIssuer,
) *LoginService {
	return &LoginService{verifiers: verifiers, issuer: issuer}
}

// Login 使用新鲜的一次性 code 登录。code 不做 Trim，避免改变平台签名内容。
func (service *LoginService) Login(
	ctx context.Context,
	providerName string,
	code string,
) (LoginResult, error) {
	providerName = strings.ToLower(strings.TrimSpace(providerName))
	verifier, ok := service.verifiers[providerName]
	if !ok {
		return LoginResult{}, ErrUnsupportedProvider
	}
	if code == "" || len(code) > 2048 || strings.TrimSpace(code) != code {
		return LoginResult{}, ErrInvalidCode
	}
	if service.issuer == nil {
		return LoginResult{}, ErrAuthUnavailable
	}

	identity, err := verifier.VerifyLoginCode(ctx, code)
	if err != nil {
		return LoginResult{}, err
	}
	if identity.Provider != providerName || identity.ProviderUID == "" {
		return LoginResult{}, ErrInvalidResponse
	}

	session, err := service.issuer.LoginVerifiedIdentity(
		ctx,
		identity.Provider,
		identity.ProviderUID,
	)
	if err != nil {
		return LoginResult{}, err
	}
	return LoginResult{Provider: providerName, LoginResult: session}, nil
}
