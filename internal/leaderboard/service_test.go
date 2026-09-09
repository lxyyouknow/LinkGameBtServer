package leaderboard

import (
	"context"
	"errors"
	"testing"
	"time"

	"linkgame-server/internal/platform"
)

type fakeStore struct {
	updated     platform.AuthorizedProfile
	invalidated bool
}

func (store *fakeStore) Global(context.Context, uint64, int) ([]Entry, Entry, error) {
	rank := 1
	entry := Entry{Rank: &rank, DisplayName: "USER000001", PlayerNumber: 1, Level: 1, IsSelf: true}
	return []Entry{entry}, entry, nil
}

func (store *fakeStore) UpdateTikTokProfile(_ context.Context, _ uint64, profile platform.AuthorizedProfile, now time.Time) (Profile, error) {
	store.updated = profile
	return Profile{Status: "authorized", DisplayName: profile.DisplayName, AvatarURL: profile.AvatarURL, UpdatedAt: now}, nil
}

func (store *fakeStore) InvalidateTikTokProfile(context.Context, uint64, time.Time) error {
	store.invalidated = true
	return nil
}

type fakeAuthorizer struct {
	profile platform.AuthorizedProfile
	err     error
}

func (authorizer fakeAuthorizer) FetchAuthorizedProfile(context.Context, string) (platform.AuthorizedProfile, error) {
	return authorizer.profile, authorizer.err
}

func TestDefaultDisplayName至少六位且不截断(t *testing.T) {
	tests := map[uint64]string{1: "USER000001", 42: "USER000042", 999999: "USER999999", 1000000: "USER1000000"}
	for number, expected := range tests {
		if actual := DefaultDisplayName(number); actual != expected {
			t.Fatalf("DefaultDisplayName(%d)=%q，期望 %q", number, actual, expected)
		}
	}
}

func TestAuthorizeTikTokProfile清洗公开资料(t *testing.T) {
	store := &fakeStore{}
	service := NewService(store, fakeAuthorizer{profile: platform.AuthorizedProfile{
		Provider: "tiktok", ProviderUID: "open-id", DisplayName: "  玩家\u0000名字  ", AvatarURL: "https://p16-sign.tiktokcdn-us.com/avatar.jpeg",
	}})
	result, err := service.AuthorizeTikTokProfile(context.Background(), 7, "one-time-code")
	if err != nil {
		t.Fatalf("AuthorizeTikTokProfile() error=%v", err)
	}
	if result.DisplayName != "玩家名字" || store.updated.DisplayName != "玩家名字" {
		t.Fatalf("昵称未正确清洗: %#v", result)
	}
	if result.AvatarURL == "" {
		t.Fatal("合法 HTTPS 头像被错误丢弃")
	}
}

func TestAuthorizeTikTokProfile拒绝非法头像但保留榜单资料(t *testing.T) {
	store := &fakeStore{}
	service := NewService(store, fakeAuthorizer{profile: platform.AuthorizedProfile{
		Provider: "tiktok", ProviderUID: "open-id", DisplayName: "Player", AvatarURL: "http://example.com/avatar.png",
	}})
	result, err := service.AuthorizeTikTokProfile(context.Background(), 7, "one-time-code")
	if err != nil {
		t.Fatalf("AuthorizeTikTokProfile() error=%v", err)
	}
	if result.AvatarURL != "" || store.updated.AvatarURL != "" {
		t.Fatalf("非 HTTPS 头像不应保存: %#v", result)
	}
}

func TestAuthorizeTikTokProfile上游撤权时清理旧资料(t *testing.T) {
	store := &fakeStore{}
	service := NewService(store, fakeAuthorizer{err: platform.ErrProfileUnauthorized})
	_, err := service.AuthorizeTikTokProfile(context.Background(), 7, "one-time-code")
	if !errors.Is(err, platform.ErrProfileUnauthorized) || !store.invalidated {
		t.Fatalf("撤权处理错误: err=%v invalidated=%v", err, store.invalidated)
	}
}
