package gm

import (
	"testing"
	"time"
)

func TestLoginAuthenticateLogoutAndExpiry(t *testing.T) {
	service := NewService(true, map[string]string{
		"goods-admin": "strong-password",
		"ops-admin":   "another-password",
	}, time.Hour)
	now := time.Date(2026, 8, 4, 0, 0, 0, 0, time.UTC)
	service.now = func() time.Time { return now }

	if _, _, err := service.Login("goods-admin", "wrong-password"); err != ErrAuthentication {
		t.Fatalf("错误密码返回 %v，期望 ErrAuthentication", err)
	}
	token, expiresAt, err := service.Login("goods-admin", "strong-password")
	if err != nil {
		t.Fatalf("Login() 返回意外错误: %v", err)
	}
	if !service.Authenticate(token) || expiresAt != now.Add(time.Hour) {
		t.Fatal("新会话未通过认证或过期时间不正确")
	}
	now = now.Add(time.Hour)
	if service.Authenticate(token) {
		t.Fatal("到期会话仍然通过认证")
	}

	token, _, err = service.Login("goods-admin", "strong-password")
	if err != nil {
		t.Fatal(err)
	}
	service.Logout(token)
	if service.Authenticate(token) {
		t.Fatal("退出后的会话仍然通过认证")
	}

	if _, _, err = service.Login("ops-admin", "another-password"); err != nil {
		t.Fatalf("第二个管理员账号无法登录: %v", err)
	}
}

func TestDisabledServiceRejectsLogin(t *testing.T) {
	service := NewService(false, map[string]string{
		"goods-admin": "strong-password",
	}, time.Hour)
	if _, _, err := service.Login("goods-admin", "strong-password"); err != ErrDisabled {
		t.Fatalf("Login() 错误=%v，期望 ErrDisabled", err)
	}
}

func TestLoginRateLimit(t *testing.T) {
	service := NewService(true, map[string]string{
		"goods-admin": "strong-password",
	}, time.Hour)
	now := time.Date(2026, 8, 5, 0, 0, 0, 0, time.UTC)
	service.now = func() time.Time { return now }

	for attempt := 1; attempt < maxLoginFailures; attempt++ {
		if _, _, err := service.Login("goods-admin", "wrong-password"); err != ErrAuthentication {
			t.Fatalf("第 %d 次错误密码返回 %v，期望 ErrAuthentication", attempt, err)
		}
	}
	if _, _, err := service.Login("goods-admin", "wrong-password"); err != ErrRateLimited {
		t.Fatalf("达到失败上限后返回 %v，期望 ErrRateLimited", err)
	}
	if _, _, err := service.Login("goods-admin", "strong-password"); err != ErrRateLimited {
		t.Fatalf("封禁期内正确密码返回 %v，期望 ErrRateLimited", err)
	}

	now = now.Add(loginBlockTime)
	if _, _, err := service.Login("goods-admin", "strong-password"); err != nil {
		t.Fatalf("封禁期结束后仍无法登录: %v", err)
	}
}
