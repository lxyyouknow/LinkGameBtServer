package platform

import (
	"context"
	"errors"
	"testing"
	"time"

	"linkgame-server/internal/auth"
)

func TestLoginVerifiesCodeBeforeIssuingSession(t *testing.T) {
	verifier := &stubVerifier{identity: VerifiedIdentity{
		Provider:    "tiktok",
		ProviderUID: "open-id",
	}}
	issuer := &stubIssuer{result: auth.LoginResult{
		Player:    auth.Player{PublicID: "public-player-id"},
		Token:     "session-token",
		ExpiresAt: time.Date(2026, 8, 3, 0, 0, 0, 0, time.UTC),
	}}
	service := NewLoginService(map[string]Verifier{"tiktok": verifier}, issuer)

	result, err := service.Login(context.Background(), "tiktok", "fresh-code")
	if err != nil {
		t.Fatalf("Login() 返回意外错误: %v", err)
	}
	if verifier.code != "fresh-code" {
		t.Fatalf("验证器收到 code = %q", verifier.code)
	}
	if issuer.provider != "tiktok" || issuer.providerUID != "open-id" {
		t.Fatalf("签发器收到错误身份: %q/%q", issuer.provider, issuer.providerUID)
	}
	if result.Provider != "tiktok" || result.Player.PublicID != "public-player-id" {
		t.Fatalf("登录结果 = %#v", result)
	}
}

func TestLoginRejectsInvalidInputBeforeVerifier(t *testing.T) {
	verifier := &stubVerifier{}
	service := NewLoginService(map[string]Verifier{"tiktok": verifier}, &stubIssuer{})

	_, err := service.Login(context.Background(), "tiktok", " code ")
	if !errors.Is(err, ErrInvalidCode) {
		t.Fatalf("错误 = %v，期望 ErrInvalidCode", err)
	}
	if verifier.called {
		t.Fatal("非法 code 不应调用平台验证器")
	}

	_, err = service.Login(context.Background(), "unknown", "code")
	if !errors.Is(err, ErrUnsupportedProvider) {
		t.Fatalf("错误 = %v，期望 ErrUnsupportedProvider", err)
	}
}

type stubVerifier struct {
	called   bool
	code     string
	identity VerifiedIdentity
	err      error
}

func (verifier *stubVerifier) VerifyLoginCode(
	_ context.Context,
	code string,
) (VerifiedIdentity, error) {
	verifier.called = true
	verifier.code = code
	return verifier.identity, verifier.err
}

type stubIssuer struct {
	provider    string
	providerUID string
	result      auth.LoginResult
	err         error
}

func (issuer *stubIssuer) LoginVerifiedIdentity(
	_ context.Context,
	provider string,
	providerUID string,
) (auth.LoginResult, error) {
	issuer.provider = provider
	issuer.providerUID = providerUID
	return issuer.result, issuer.err
}
