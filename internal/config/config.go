// Package config 负责读取并校验服务运行所需的环境变量。
package config

import (
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"
)

var validAppEnvironments = []string{
	"development",
	"test",
	"staging",
	"production",
}

// Config 是服务启动所需的基础配置。
type Config struct {
	AdPolicyPath                    string
	AppEnv                          string
	HTTPAddr                        string
	LogLevel                        slog.Level
	CORSAllowedOrigins              []string
	EnableTestAccount               bool
	EnableTikTokLogin               bool
	TikTokHomeShortcutEnabled       bool
	TikTokProfileRevisitEnabled     bool
	TikTokProfileRevisitJumpEnabled bool
	SessionTTL                      time.Duration
	TikTok                          TikTokConfig
	Analytics                       AnalyticsConfig
	MySQL                           MySQLConfig
}

// AnalyticsConfig 是统计采集和内部 GM 后台配置。
// 账号密码只允许由服务器环境变量注入，不进入客户端或仓库。
type AnalyticsConfig struct {
	Enabled    bool
	GMUsers    map[string]string
	SessionTTL time.Duration
	Location   *time.Location
}

// TikTokConfig 是服务端 TikTok OAuth 配置。密钥只能通过环境变量注入。
type TikTokConfig struct {
	ClientKey    string
	ClientSecret string
	OAuthTimeout time.Duration
}

// MySQLConfig 是 MySQL 地址和连接池参数。
type MySQLConfig struct {
	DSN             string
	MaxOpenConns    int
	MaxIdleConns    int
	ConnMaxLifetime time.Duration
	ConnMaxIdleTime time.Duration
}

// Load 从环境变量读取配置。关键配置缺失或格式不合法时直接返回错误，
// 避免服务带着错误配置继续运行。
func Load() (Config, error) {
	appEnv, err := requiredEnv("APP_ENV")
	if err != nil {
		return Config{}, err
	}
	if !slices.Contains(validAppEnvironments, appEnv) {
		return Config{}, fmt.Errorf(
			"环境变量 APP_ENV=%q 不合法，可选值为 %s",
			appEnv,
			strings.Join(validAppEnvironments, "、"),
		)
	}

	httpAddr, err := requiredEnv("HTTP_ADDR")
	if err != nil {
		return Config{}, err
	}
	if _, _, err := net.SplitHostPort(httpAddr); err != nil {
		return Config{}, fmt.Errorf(
			"环境变量 HTTP_ADDR=%q 格式不合法，应类似 :8080 或 127.0.0.1:8080: %w",
			httpAddr,
			err,
		)
	}

	logLevel, err := parseLogLevel(envOrDefault("LOG_LEVEL", "info"))
	if err != nil {
		return Config{}, err
	}

	corsOriginsValue, err := requiredEnv("CORS_ALLOWED_ORIGINS")
	if err != nil {
		return Config{}, err
	}
	corsAllowedOrigins, err := parseCORSAllowedOrigins(
		corsOriginsValue,
		appEnv,
	)
	if err != nil {
		return Config{}, err
	}

	enableTestAccount, err := booleanEnv(
		"ENABLE_TEST_ACCOUNT_LOGIN",
		appEnv == "development" || appEnv == "test",
	)
	if err != nil {
		return Config{}, err
	}
	enableTikTokLogin, err := booleanEnv("ENABLE_TIKTOK_LOGIN", false)
	if err != nil {
		return Config{}, err
	}
	tikTokHomeShortcutEnabled, err := booleanEnv("ENABLE_TIKTOK_HOME_SHORTCUT", true)
	if err != nil {
		return Config{}, err
	}
	tikTokProfileRevisitEnabled, err := booleanEnv("ENABLE_TIKTOK_PROFILE_REVISIT", true)
	if err != nil {
		return Config{}, err
	}
	tikTokProfileRevisitJumpEnabled, err := booleanEnv("TIKTOK_PROFILE_REVISIT_JUMP_ENABLED", true)
	if err != nil {
		return Config{}, err
	}
	tikTokConfig, err := loadTikTokConfig(enableTikTokLogin)
	if err != nil {
		return Config{}, err
	}
	analyticsConfig, err := loadAnalyticsConfig()
	if err != nil {
		return Config{}, err
	}

	sessionTTL, err := positiveDurationEnv("SESSION_TTL", 30*24*time.Hour)
	if err != nil {
		return Config{}, err
	}
	if sessionTTL > 90*24*time.Hour {
		return Config{}, fmt.Errorf(
			"环境变量 SESSION_TTL=%q 不能超过 2160h（90 天）",
			sessionTTL,
		)
	}

	mysqlConfig, err := LoadMySQLConfig()
	if err != nil {
		return Config{}, err
	}

	return Config{
		AdPolicyPath:                    envOrDefault("AD_POLICY_CONFIG_PATH", "deploy/config/ad-policy.json"),
		AppEnv:                          appEnv,
		HTTPAddr:                        httpAddr,
		LogLevel:                        logLevel,
		CORSAllowedOrigins:              corsAllowedOrigins,
		EnableTestAccount:               enableTestAccount,
		EnableTikTokLogin:               enableTikTokLogin,
		TikTokHomeShortcutEnabled:       tikTokHomeShortcutEnabled,
		TikTokProfileRevisitEnabled:     tikTokProfileRevisitEnabled,
		TikTokProfileRevisitJumpEnabled: tikTokProfileRevisitJumpEnabled,
		SessionTTL:                      sessionTTL,
		TikTok:                          tikTokConfig,
		Analytics:                       analyticsConfig,
		MySQL:                           mysqlConfig,
	}, nil
}

func loadAnalyticsConfig() (AnalyticsConfig, error) {
	enabled, err := booleanEnv("ANALYTICS_ENABLED", false)
	if err != nil {
		return AnalyticsConfig{}, err
	}
	timeZoneName := envOrDefault("ANALYTICS_TIMEZONE", "Asia/Shanghai")
	location, err := time.LoadLocation(timeZoneName)
	if err != nil {
		return AnalyticsConfig{}, fmt.Errorf(
			"环境变量 ANALYTICS_TIMEZONE=%q 不是有效时区: %w",
			timeZoneName,
			err,
		)
	}
	sessionTTL, err := positiveDurationEnv(
		"ANALYTICS_GM_SESSION_TTL",
		12*time.Hour,
	)
	if err != nil {
		return AnalyticsConfig{}, err
	}
	if sessionTTL > 7*24*time.Hour {
		return AnalyticsConfig{}, fmt.Errorf(
			"环境变量 ANALYTICS_GM_SESSION_TTL 不能超过 168h",
		)
	}

	gmUsers, err := loadAnalyticsGMUsers(enabled)
	if err != nil {
		return AnalyticsConfig{}, err
	}

	return AnalyticsConfig{
		Enabled:    enabled,
		GMUsers:    gmUsers,
		SessionTTL: sessionTTL,
		Location:   location,
	}, nil
}

func loadAnalyticsGMUsers(enabled bool) (map[string]string, error) {
	users := make(map[string]string)
	if !enabled {
		return users, nil
	}

	multipleUsers := strings.TrimSpace(os.Getenv("ANALYTICS_GM_USERS"))
	if multipleUsers != "" {
		entries := strings.Split(multipleUsers, ",")
		if len(entries) > 32 {
			return nil, fmt.Errorf("环境变量 ANALYTICS_GM_USERS 最多允许配置 32 个账号")
		}
		for _, rawEntry := range entries {
			parts := strings.SplitN(strings.TrimSpace(rawEntry), ":", 2)
			if len(parts) != 2 {
				return nil, fmt.Errorf("环境变量 ANALYTICS_GM_USERS 格式不合法，应为 账号:密码,账号:密码")
			}
			account := strings.TrimSpace(parts[0])
			password := parts[1]
			if err := validateAnalyticsGMUser(account, password, 2, 6); err != nil {
				return nil, fmt.Errorf("环境变量 ANALYTICS_GM_USERS: %w", err)
			}
			if _, exists := users[account]; exists {
				return nil, fmt.Errorf("环境变量 ANALYTICS_GM_USERS 包含重复账号 %q", account)
			}
			users[account] = password
		}
		return users, nil
	}

	// 兼容旧部署：未配置多账号时，继续读取原有单账号环境变量。
	account := strings.TrimSpace(os.Getenv("ANALYTICS_GM_ACCOUNT"))
	password := os.Getenv("ANALYTICS_GM_PASSWORD")
	if err := validateAnalyticsGMUser(account, password, 3, 12); err != nil {
		return nil, fmt.Errorf("ANALYTICS_ENABLED=true 时旧版 GM 凭据不合法: %w", err)
	}
	users[account] = password
	return users, nil
}

func validateAnalyticsGMUser(account string, password string, minAccountLength int, minPasswordLength int) error {
	if len(account) < minAccountLength || len(account) > 64 {
		return fmt.Errorf("账号必须为 %d～64 个字符", minAccountLength)
	}
	if len(password) < minPasswordLength || len(password) > 128 {
		return fmt.Errorf("密码必须为 %d～128 个字符", minPasswordLength)
	}
	return nil
}

func loadTikTokConfig(enabled bool) (TikTokConfig, error) {
	oauthTimeout, err := positiveDurationEnv("TIKTOK_OAUTH_TIMEOUT", 5*time.Second)
	if err != nil {
		return TikTokConfig{}, err
	}
	if oauthTimeout > 15*time.Second {
		return TikTokConfig{}, fmt.Errorf(
			"环境变量 TIKTOK_OAUTH_TIMEOUT=%q 不能超过 15s",
			oauthTimeout,
		)
	}

	clientKey := strings.TrimSpace(os.Getenv("TIKTOK_CLIENT_KEY"))
	clientSecret := strings.TrimSpace(os.Getenv("TIKTOK_CLIENT_SECRET"))
	if enabled && clientKey == "" {
		return TikTokConfig{}, fmt.Errorf(
			"ENABLE_TIKTOK_LOGIN=true 时缺少必填环境变量 TIKTOK_CLIENT_KEY",
		)
	}
	if enabled && clientSecret == "" {
		return TikTokConfig{}, fmt.Errorf(
			"ENABLE_TIKTOK_LOGIN=true 时缺少必填环境变量 TIKTOK_CLIENT_SECRET",
		)
	}

	return TikTokConfig{
		ClientKey:    clientKey,
		ClientSecret: clientSecret,
		OAuthTimeout: oauthTimeout,
	}, nil
}

func parseCORSAllowedOrigins(
	value string,
	appEnv string,
) ([]string, error) {
	origins := make([]string, 0)
	seen := make(map[string]struct{})
	for _, item := range strings.Split(value, ",") {
		rawOrigin := strings.TrimSpace(item)
		if rawOrigin == "" {
			return nil, fmt.Errorf(
				"环境变量 CORS_ALLOWED_ORIGINS 包含空来源",
			)
		}
		if rawOrigin == "*" {
			return nil, fmt.Errorf(
				"环境变量 CORS_ALLOWED_ORIGINS 禁止使用通配符 *",
			)
		}

		parsed, err := url.Parse(rawOrigin)
		if err != nil ||
			(parsed.Scheme != "http" && parsed.Scheme != "https") ||
			parsed.Host == "" ||
			parsed.User != nil ||
			parsed.Path != "" ||
			parsed.RawQuery != "" ||
			parsed.Fragment != "" {
			return nil, fmt.Errorf(
				"环境变量 CORS_ALLOWED_ORIGINS 中的来源 %q 不合法，应类似 http://localhost:7456 或 https://game.example.com",
				rawOrigin,
			)
		}
		if appEnv == "production" && parsed.Scheme != "https" {
			return nil, fmt.Errorf(
				"生产环境 CORS 来源 %q 必须使用 HTTPS",
				rawOrigin,
			)
		}

		normalizedOrigin := strings.ToLower(parsed.Scheme) +
			"://" +
			strings.ToLower(parsed.Host)
		if _, exists := seen[normalizedOrigin]; exists {
			continue
		}
		seen[normalizedOrigin] = struct{}{}
		origins = append(origins, normalizedOrigin)
	}

	if len(origins) == 0 {
		return nil, fmt.Errorf(
			"环境变量 CORS_ALLOWED_ORIGINS 至少需要一个明确来源",
		)
	}
	return origins, nil
}

// LoadMySQLConfig 单独读取数据库配置，供 API 服务和迁移命令共同使用。
func LoadMySQLConfig() (MySQLConfig, error) {
	dsn, err := requiredEnv("MYSQL_DSN")
	if err != nil {
		return MySQLConfig{}, err
	}

	maxOpenConns, err := positiveIntEnv("MYSQL_MAX_OPEN_CONNS", 20)
	if err != nil {
		return MySQLConfig{}, err
	}
	maxIdleConns, err := nonNegativeIntEnv("MYSQL_MAX_IDLE_CONNS", 10)
	if err != nil {
		return MySQLConfig{}, err
	}
	if maxIdleConns > maxOpenConns {
		return MySQLConfig{}, fmt.Errorf(
			"环境变量 MYSQL_MAX_IDLE_CONNS=%d 不能大于 MYSQL_MAX_OPEN_CONNS=%d",
			maxIdleConns,
			maxOpenConns,
		)
	}

	connMaxLifetime, err := positiveDurationEnv(
		"MYSQL_CONN_MAX_LIFETIME",
		30*time.Minute,
	)
	if err != nil {
		return MySQLConfig{}, err
	}
	connMaxIdleTime, err := positiveDurationEnv(
		"MYSQL_CONN_MAX_IDLE_TIME",
		5*time.Minute,
	)
	if err != nil {
		return MySQLConfig{}, err
	}

	return MySQLConfig{
		DSN:             dsn,
		MaxOpenConns:    maxOpenConns,
		MaxIdleConns:    maxIdleConns,
		ConnMaxLifetime: connMaxLifetime,
		ConnMaxIdleTime: connMaxIdleTime,
	}, nil
}

func requiredEnv(name string) (string, error) {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return "", fmt.Errorf("缺少必填环境变量 %s", name)
	}
	return value, nil
}

func envOrDefault(name string, fallback string) string {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback
	}
	return value
}

func booleanEnv(name string, fallback bool) (bool, error) {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback, nil
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return false, fmt.Errorf(
			"环境变量 %s=%q 必须是 true 或 false",
			name,
			value,
		)
	}
	return parsed, nil
}

func positiveIntEnv(name string, fallback int) (int, error) {
	value := envOrDefault(name, strconv.Itoa(fallback))
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed <= 0 {
		return 0, fmt.Errorf("环境变量 %s=%q 必须是大于 0 的整数", name, value)
	}
	return parsed, nil
}

func nonNegativeIntEnv(name string, fallback int) (int, error) {
	value := envOrDefault(name, strconv.Itoa(fallback))
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed < 0 {
		return 0, fmt.Errorf("环境变量 %s=%q 必须是大于或等于 0 的整数", name, value)
	}
	return parsed, nil
}

func positiveDurationEnv(name string, fallback time.Duration) (time.Duration, error) {
	value := envOrDefault(name, fallback.String())
	parsed, err := time.ParseDuration(value)
	if err != nil || parsed <= 0 {
		return 0, fmt.Errorf(
			"环境变量 %s=%q 必须是大于 0 的 Go 时长，例如 30m 或 5s",
			name,
			value,
		)
	}
	return parsed, nil
}

func parseLogLevel(value string) (slog.Level, error) {
	switch strings.ToLower(value) {
	case "debug":
		return slog.LevelDebug, nil
	case "info":
		return slog.LevelInfo, nil
	case "warn":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	default:
		return 0, fmt.Errorf(
			"环境变量 LOG_LEVEL=%q 不合法，可选值为 debug、info、warn、error",
			value,
		)
	}
}
