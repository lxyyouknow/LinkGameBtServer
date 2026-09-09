package tiktok

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"linkgame-server/internal/platform"
)

func TestVerifyLoginCodeReturnsOnlyStableIdentity(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(
		writer http.ResponseWriter,
		request *http.Request,
	) {
		if request.Method != http.MethodPost {
			t.Fatalf("请求方法 = %s，期望 POST", request.Method)
		}
		if contentType := request.Header.Get("Content-Type"); contentType != "application/x-www-form-urlencoded" {
			t.Fatalf("Content-Type = %q，期望表单", contentType)
		}
		if request.Header.Get("Cache-Control") != "no-cache" {
			t.Fatal("OAuth 请求缺少 Cache-Control: no-cache")
		}
		if err := request.ParseForm(); err != nil {
			t.Fatalf("解析请求表单失败: %v", err)
		}
		want := map[string]string{
			"client_key":    "client-key",
			"client_secret": "client-secret",
			"code":          "one-time-code",
			"grant_type":    "authorization_code",
		}
		for key, expected := range want {
			if actual := request.Form.Get(key); actual != expected {
				t.Fatalf("表单 %s = %q，期望 %q", key, actual, expected)
			}
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{
			"open_id":"stable-open-id",
			"access_token":"must-not-leave-client",
			"refresh_token":"must-not-leave-client"
		}`))
	}))
	defer server.Close()

	client := newClient("client-key", "client-secret", server.URL, time.Second)
	identity, err := client.VerifyLoginCode(context.Background(), "one-time-code")
	if err != nil {
		t.Fatalf("VerifyLoginCode() 返回意外错误: %v", err)
	}
	if identity.Provider != "tiktok" || identity.ProviderUID != "stable-open-id" {
		t.Fatalf("身份 = %#v，不符合预期", identity)
	}
}

func TestVerifyLoginCodeMapsOAuthErrors(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		body       string
		want       error
	}{
		{"一次性凭证无效", http.StatusBadRequest, `{"error":"invalid_grant"}`, platform.ErrCodeInvalid},
		{"客户端密钥错误", http.StatusUnauthorized, `{"error":"invalid_client"}`, platform.ErrAuthConfiguration},
		{"平台限流", http.StatusTooManyRequests, `{"error":"rate_limit_exceeded"}`, platform.ErrAuthUnavailable},
		{"平台故障", http.StatusBadGateway, `{}`, platform.ErrAuthUnavailable},
		{"成功响应缺少OpenID", http.StatusOK, `{"access_token":"secret"}`, platform.ErrInvalidResponse},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(
				writer http.ResponseWriter,
				_ *http.Request,
			) {
				writer.Header().Set("Content-Type", "application/json")
				writer.WriteHeader(test.statusCode)
				_, _ = writer.Write([]byte(test.body))
			}))
			defer server.Close()

			client := newClient("key", "secret", server.URL, time.Second)
			_, err := client.VerifyLoginCode(context.Background(), "code")
			if !errors.Is(err, test.want) {
				t.Fatalf("错误 = %v，期望 %v", err, test.want)
			}
		})
	}
}

func TestFetchAuthorizedProfile只请求授权公开字段(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/token":
			_, _ = writer.Write([]byte(`{"open_id":"stable-open-id","access_token":"secret-token","scope":"user.info.basic"}`))
		case "/user":
			if request.Method != http.MethodGet || request.URL.Query().Get("fields") != "open_id,display_name,avatar_url" {
				t.Fatalf("资料请求不符合官方字段约束: %s", request.URL.String())
			}
			if request.Header.Get("Authorization") != "Bearer secret-token" {
				t.Fatalf("资料请求未携带服务端 access token")
			}
			_, _ = writer.Write([]byte(`{"data":{"user":{"open_id":"stable-open-id","display_name":"PairMaster","avatar_url":"https://example.com/avatar.png"}},"error":{"code":"ok"}}`))
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()
	client := newClientWithEndpoints("key", "secret", server.URL+"/token", server.URL+"/user", time.Second)
	profile, err := client.FetchAuthorizedProfile(context.Background(), "one-time-code")
	if err != nil {
		t.Fatalf("FetchAuthorizedProfile() error=%v", err)
	}
	if profile.ProviderUID != "stable-open-id" || profile.DisplayName != "PairMaster" || profile.AvatarURL == "" {
		t.Fatalf("资料响应错误: %#v", profile)
	}
}

func TestFetchAuthorizedProfile缺少Scope时不请求资料(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"open_id":"stable-open-id","access_token":"secret-token","scope":"user.info.open_id"}`))
	}))
	defer server.Close()
	client := newClientWithEndpoints("key", "secret", server.URL, server.URL, time.Second)
	_, err := client.FetchAuthorizedProfile(context.Background(), "one-time-code")
	if !errors.Is(err, platform.ErrScopeDenied) {
		t.Fatalf("错误=%v，期望 ErrScopeDenied", err)
	}
}
