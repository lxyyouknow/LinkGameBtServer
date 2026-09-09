// Package tiktok 封装 TikTok OAuth 登录凭证交换。
package tiktok

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"linkgame-server/internal/platform"
)

const tokenEndpoint = "https://open.tiktokapis.com/v2/oauth/token/"
const userInfoEndpoint = "https://open.tiktokapis.com/v2/user/info/"
const maxResponseBodyBytes int64 = 1024 * 1024

// Client 只负责使用一次性 code 换取 TikTok OpenID。
// access_token 与 refresh_token 不会返回到调用方，也不会持久化。
type Client struct {
	clientKey        string
	clientSecret     string
	tokenEndpoint    string
	userInfoEndpoint string
	httpClient       *http.Client
}

// NewClient 创建 TikTok OAuth 客户端。
func NewClient(clientKey string, clientSecret string, timeout time.Duration) *Client {
	return newClientWithEndpoints(clientKey, clientSecret, tokenEndpoint, userInfoEndpoint, timeout)
}

func newClient(
	clientKey string,
	clientSecret string,
	endpoint string,
	timeout time.Duration,
) *Client {
	return newClientWithEndpoints(clientKey, clientSecret, endpoint, userInfoEndpoint, timeout)
}

func newClientWithEndpoints(
	clientKey string,
	clientSecret string,
	tokenURL string,
	userInfoURL string,
	timeout time.Duration,
) *Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.DialContext = (&net.Dialer{
		Timeout:   min(timeout, 5*time.Second),
		KeepAlive: 30 * time.Second,
	}).DialContext
	transport.TLSHandshakeTimeout = min(timeout, 5*time.Second)
	transport.ResponseHeaderTimeout = timeout
	return &Client{
		clientKey:        clientKey,
		clientSecret:     clientSecret,
		tokenEndpoint:    tokenURL,
		userInfoEndpoint: userInfoURL,
		httpClient: &http.Client{
			Timeout:   timeout,
			Transport: transport,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}
}

// VerifyLoginCode 调用 TikTok 官方 token endpoint，并且只保留稳定 OpenID。
func (client *Client) VerifyLoginCode(
	ctx context.Context,
	code string,
) (platform.VerifiedIdentity, error) {
	token, err := client.exchangeCode(ctx, code)
	if err != nil {
		return platform.VerifiedIdentity{}, err
	}
	return platform.VerifiedIdentity{
		Provider:    "tiktok",
		ProviderUID: token.OpenID,
	}, nil
}

type oauthToken struct {
	OpenID      string
	AccessToken string
	Scope       string
}

func (client *Client) exchangeCode(ctx context.Context, code string) (oauthToken, error) {
	form := url.Values{
		"client_key":    {client.clientKey},
		"client_secret": {client.clientSecret},
		"code":          {code},
		"grant_type":    {"authorization_code"},
	}
	request, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		client.tokenEndpoint,
		strings.NewReader(form.Encode()),
	)
	if err != nil {
		return oauthToken{}, fmt.Errorf(
			"创建 TikTok OAuth 请求失败: %w",
			platform.ErrAuthUnavailable,
		)
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Cache-Control", "no-cache")

	response, err := client.httpClient.Do(request)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return oauthToken{}, err
		}
		return oauthToken{}, fmt.Errorf(
			"TikTok OAuth 请求失败: %w",
			platform.ErrAuthUnavailable,
		)
	}
	defer response.Body.Close()
	mediaType, _, err := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		return oauthToken{}, platform.ErrInvalidResponse
	}

	body, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBodyBytes+1))
	if err != nil || int64(len(body)) > maxResponseBodyBytes {
		return oauthToken{}, platform.ErrInvalidResponse
	}

	var payload struct {
		OpenID      string `json:"open_id"`
		AccessToken string `json:"access_token"`
		Scope       string `json:"scope"`
		Error       string `json:"error"`
		ErrorCode   string `json:"error_code"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return oauthToken{}, platform.ErrInvalidResponse
	}

	if response.StatusCode < http.StatusOK || response.StatusCode >= 300 ||
		payload.Error != "" || payload.ErrorCode != "" {
		return oauthToken{}, oauthError(
			response.StatusCode,
			firstNonEmpty(payload.Error, payload.ErrorCode),
		)
	}
	if payload.OpenID == "" || len(payload.OpenID) > 191 ||
		strings.TrimSpace(payload.OpenID) != payload.OpenID {
		return oauthToken{}, platform.ErrInvalidResponse
	}
	return oauthToken{OpenID: payload.OpenID, AccessToken: payload.AccessToken, Scope: payload.Scope}, nil
}

// FetchAuthorizedProfile 消费显式授权 code，并只把经授权的公开资料交给业务层。
func (client *Client) FetchAuthorizedProfile(ctx context.Context, code string) (platform.AuthorizedProfile, error) {
	token, err := client.exchangeCode(ctx, code)
	if err != nil {
		return platform.AuthorizedProfile{}, err
	}
	if !hasScope(token.Scope, "user.info.basic") || token.AccessToken == "" || len(token.AccessToken) > 4096 {
		return platform.AuthorizedProfile{}, platform.ErrScopeDenied
	}

	endpoint, err := url.Parse(client.userInfoEndpoint)
	if err != nil {
		return platform.AuthorizedProfile{}, platform.ErrInvalidResponse
	}
	query := endpoint.Query()
	query.Set("fields", "open_id,display_name,avatar_url")
	endpoint.RawQuery = query.Encode()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return platform.AuthorizedProfile{}, platform.ErrAuthUnavailable
	}
	request.Header.Set("Authorization", "Bearer "+token.AccessToken)
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Cache-Control", "no-cache")
	response, err := client.httpClient.Do(request)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return platform.AuthorizedProfile{}, err
		}
		return platform.AuthorizedProfile{}, platform.ErrAuthUnavailable
	}
	defer response.Body.Close()
	mediaType, _, err := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		return platform.AuthorizedProfile{}, platform.ErrInvalidResponse
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBodyBytes+1))
	if err != nil || int64(len(body)) > maxResponseBodyBytes {
		return platform.AuthorizedProfile{}, platform.ErrInvalidResponse
	}
	var payload struct {
		Data struct {
			User struct {
				OpenID      string `json:"open_id"`
				DisplayName string `json:"display_name"`
				AvatarURL   string `json:"avatar_url"`
			} `json:"user"`
		} `json:"data"`
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return platform.AuthorizedProfile{}, platform.ErrInvalidResponse
	}
	if response.StatusCode == http.StatusUnauthorized {
		return platform.AuthorizedProfile{}, platform.ErrProfileUnauthorized
	}
	if response.StatusCode == http.StatusTooManyRequests || response.StatusCode >= 500 {
		return platform.AuthorizedProfile{}, platform.ErrAuthUnavailable
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 || (payload.Error.Code != "" && payload.Error.Code != "ok") {
		return platform.AuthorizedProfile{}, platform.ErrInvalidResponse
	}
	if payload.Data.User.OpenID == "" || payload.Data.User.OpenID != token.OpenID {
		return platform.AuthorizedProfile{}, platform.ErrInvalidResponse
	}
	return platform.AuthorizedProfile{
		Provider: "tiktok", ProviderUID: token.OpenID,
		DisplayName: payload.Data.User.DisplayName, AvatarURL: payload.Data.User.AvatarURL,
	}, nil
}

func hasScope(value, expected string) bool {
	for _, scope := range strings.Split(value, ",") {
		if strings.TrimSpace(scope) == expected {
			return true
		}
	}
	return false
}

func oauthError(statusCode int, code string) error {
	switch strings.ToLower(code) {
	case "invalid_grant", "invalid_request":
		return platform.ErrCodeInvalid
	case "invalid_client", "unauthorized_client":
		return platform.ErrAuthConfiguration
	}
	if statusCode == http.StatusTooManyRequests || statusCode >= 500 {
		return platform.ErrAuthUnavailable
	}
	if statusCode == http.StatusUnauthorized || statusCode == http.StatusBadRequest {
		return platform.ErrCodeInvalid
	}
	return platform.ErrInvalidResponse
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
