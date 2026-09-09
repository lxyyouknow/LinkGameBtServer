package auth

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"testing"
	"time"
)

func TestLoginGuestGeneratesSecureSession(t *testing.T) {
	fixedNow := time.Date(2026, 7, 27, 3, 0, 0, 0, time.UTC)
	store := &stubStore{
		loginPlayer: Player{
			ID:       42,
			PublicID: "01010101-0101-4101-8101-010101010101",
		},
	}
	service := NewService(store, 30*24*time.Hour)
	service.now = func() time.Time { return fixedNow }
	service.random = bytes.NewReader(bytes.Repeat([]byte{1}, 48))

	result, err := service.LoginGuest(
		context.Background(),
		"device-1234567890",
	)
	if err != nil {
		t.Fatalf("LoginGuest() 返回了意外错误: %v", err)
	}

	if result.Player != store.loginPlayer {
		t.Fatalf("玩家 = %#v，期望 %#v", result.Player, store.loginPlayer)
	}
	if result.ExpiresAt != fixedNow.Add(30*24*time.Hour) {
		t.Fatalf("过期时间 = %v，不符合 30 天会话有效期", result.ExpiresAt)
	}

	tokenBytes, err := base64.RawURLEncoding.DecodeString(result.Token)
	if err != nil {
		t.Fatalf("Token 不是合法 Base64 URL 编码: %v", err)
	}
	if len(tokenBytes) != 32 {
		t.Fatalf("Token 随机字节长度 = %d，期望 32", len(tokenBytes))
	}

	expectedHash := sha256.Sum256([]byte(result.Token))
	if store.loginParams.TokenHash != expectedHash {
		t.Fatal("传给存储层的 Token 哈希不正确")
	}
	if store.loginParams.Provider != "guest" ||
		store.loginParams.ProviderUID != "device-1234567890" {
		t.Fatalf("游客身份参数不正确: %#v", store.loginParams)
	}
	if store.loginParams.PublicID != "01010101-0101-4101-8101-010101010101" {
		t.Fatalf("生成的 UUID v4 = %q，不符合预期", store.loginParams.PublicID)
	}
}

func TestLoginGuestRejectsInvalidInstallationID(t *testing.T) {
	store := &stubStore{}
	service := NewService(store, time.Hour)

	_, err := service.LoginGuest(context.Background(), "too-short")
	if !errors.Is(err, ErrInvalidInstallationID) {
		t.Fatalf("LoginGuest() 错误 = %v，期望 ErrInvalidInstallationID", err)
	}
	if store.loginCalled {
		t.Fatal("非法 installationId 不应访问数据库")
	}
}

func TestLoginTestAccountNormalizesAccount(t *testing.T) {
	fixedNow := time.Date(2026, 7, 27, 3, 0, 0, 0, time.UTC)
	store := &stubStore{
		loginPlayer: Player{
			ID:       43,
			PublicID: "02020202-0202-4202-8202-020202020202",
		},
	}
	service := NewService(store, 30*24*time.Hour)
	service.now = func() time.Time { return fixedNow }
	service.random = bytes.NewReader(bytes.Repeat([]byte{2}, 48))

	result, err := service.LoginTestAccount(context.Background(), "  Lxy_01  ")
	if err != nil {
		t.Fatalf("LoginTestAccount() 返回了意外错误: %v", err)
	}
	if result.Account != "lxy_01" {
		t.Fatalf("规范化账号 = %q，期望 lxy_01", result.Account)
	}
	if result.Player != store.loginPlayer {
		t.Fatalf("玩家 = %#v，期望 %#v", result.Player, store.loginPlayer)
	}
	if store.loginParams.Provider != "test_account" ||
		store.loginParams.ProviderUID != "lxy_01" {
		t.Fatalf("测试账号身份参数不正确: %#v", store.loginParams)
	}
}

func TestLoginTestAccountRejectsInvalidAccount(t *testing.T) {
	tests := []string{
		"ab",
		"账号",
		"has space",
		"_starts_wrong",
		"abcdefghijklmnopqrstuvwxyz1234567",
	}
	for _, account := range tests {
		t.Run(account, func(t *testing.T) {
			store := &stubStore{}
			service := NewService(store, time.Hour)

			_, err := service.LoginTestAccount(context.Background(), account)
			if !errors.Is(err, ErrInvalidTestAccount) {
				t.Fatalf(
					"LoginTestAccount() 错误 = %v，期望 ErrInvalidTestAccount",
					err,
				)
			}
			if store.loginCalled {
				t.Fatal("非法测试账号不应访问数据库")
			}
		})
	}
}

func TestLoginVerifiedIdentityUsesExactPlatformIdentity(t *testing.T) {
	store := &stubStore{loginPlayer: Player{ID: 44, PublicID: "public-id"}}
	service := NewService(store, time.Hour)
	service.random = bytes.NewReader(bytes.Repeat([]byte{3}, 48))

	_, err := service.LoginVerifiedIdentity(
		context.Background(),
		"tiktok",
		"stable-open-id",
	)
	if err != nil {
		t.Fatalf("LoginVerifiedIdentity() 返回意外错误: %v", err)
	}
	if store.loginParams.Provider != "tiktok" ||
		store.loginParams.ProviderUID != "stable-open-id" {
		t.Fatalf("身份参数 = %#v", store.loginParams)
	}
}

func TestLoginVerifiedIdentityRejectsUntrustedShape(t *testing.T) {
	for _, identity := range [][2]string{
		{"TikTok", "open-id"},
		{"tiktok", " open-id "},
		{"tiktok", ""},
	} {
		store := &stubStore{}
		service := NewService(store, time.Hour)
		_, err := service.LoginVerifiedIdentity(
			context.Background(),
			identity[0],
			identity[1],
		)
		if !errors.Is(err, ErrInvalidVerifiedIdentity) {
			t.Fatalf("身份 %#v 的错误 = %v", identity, err)
		}
		if store.loginCalled {
			t.Fatal("非法平台身份不应访问数据库")
		}
	}
}

func TestAuthenticateToken(t *testing.T) {
	expectedPlayer := Player{
		ID:       7,
		PublicID: "player-public-id",
	}
	store := &stubStore{authPlayer: expectedPlayer}
	service := NewService(store, time.Hour)
	fixedNow := time.Date(2026, 7, 27, 3, 0, 0, 0, time.UTC)
	service.now = func() time.Time { return fixedNow }
	token := "12345678901234567890123456789012"

	player, err := service.AuthenticateToken(context.Background(), token)
	if err != nil {
		t.Fatalf("AuthenticateToken() 返回了意外错误: %v", err)
	}
	if player != expectedPlayer {
		t.Fatalf("玩家 = %#v，期望 %#v", player, expectedPlayer)
	}
	if store.authHash != sha256.Sum256([]byte(token)) {
		t.Fatal("认证时没有使用 Token 的 SHA-256")
	}
	if store.authNow != fixedNow {
		t.Fatalf("认证时间 = %v，期望 %v", store.authNow, fixedNow)
	}
}

func TestAuthenticateTokenRejectsInvalidToken(t *testing.T) {
	store := &stubStore{}
	service := NewService(store, time.Hour)

	_, err := service.AuthenticateToken(context.Background(), "short")
	if !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("AuthenticateToken() 错误 = %v，期望 ErrUnauthenticated", err)
	}
	if store.authCalled {
		t.Fatal("明显非法的 Token 不应访问数据库")
	}
}

type stubStore struct {
	loginCalled bool
	loginParams IdentityLoginParams
	loginPlayer Player
	loginErr    error

	authCalled bool
	authHash   [sha256.Size]byte
	authNow    time.Time
	authPlayer Player
	authErr    error
}

func (store *stubStore) LoginIdentity(
	_ context.Context,
	params IdentityLoginParams,
) (Player, error) {
	store.loginCalled = true
	store.loginParams = params
	return store.loginPlayer, store.loginErr
}

func (store *stubStore) AuthenticateSession(
	_ context.Context,
	tokenHash [sha256.Size]byte,
	now time.Time,
) (Player, error) {
	store.authCalled = true
	store.authHash = tokenHash
	store.authNow = now
	return store.authPlayer, store.authErr
}
