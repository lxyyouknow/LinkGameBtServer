package config

import (
	"log/slog"
	"strings"
	"testing"
	"time"
)

func TestLoad(t *testing.T) {
	t.Setenv("APP_ENV", "development")
	t.Setenv("HTTP_ADDR", ":8080")
	t.Setenv("LOG_LEVEL", "debug")
	t.Setenv("ENABLE_TEST_ACCOUNT_LOGIN", "")
	t.Setenv("ENABLE_TIKTOK_LOGIN", "")
	t.Setenv("TIKTOK_CLIENT_KEY", "")
	t.Setenv("TIKTOK_CLIENT_SECRET", "")
	t.Setenv("TIKTOK_OAUTH_TIMEOUT", "")
	t.Setenv("ENABLE_TIKTOK_HOME_SHORTCUT", "")
	t.Setenv("ENABLE_TIKTOK_PROFILE_REVISIT", "")
	t.Setenv("TIKTOK_PROFILE_REVISIT_JUMP_ENABLED", "")
	t.Setenv(
		"CORS_ALLOWED_ORIGINS",
		"http://localhost:7456,http://127.0.0.1:7456",
	)
	t.Setenv("MYSQL_DSN", "user:password@tcp(localhost:3306)/database")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() 返回了意外错误: %v", err)
	}

	if cfg.AppEnv != "development" {
		t.Fatalf("AppEnv = %q，期望 development", cfg.AppEnv)
	}
	if cfg.HTTPAddr != ":8080" {
		t.Fatalf("HTTPAddr = %q，期望 :8080", cfg.HTTPAddr)
	}
	if cfg.LogLevel != slog.LevelDebug {
		t.Fatalf("LogLevel = %v，期望 %v", cfg.LogLevel, slog.LevelDebug)
	}
	if !cfg.EnableTestAccount {
		t.Fatal("development 默认应开放测试账号登录")
	}
	if cfg.SessionTTL != 30*24*time.Hour {
		t.Fatalf("SessionTTL = %v，期望 30 天", cfg.SessionTTL)
	}
	if cfg.EnableTikTokLogin {
		t.Fatal("TikTok 登录默认应保持关闭")
	}
	if !cfg.TikTokHomeShortcutEnabled ||
		!cfg.TikTokProfileRevisitEnabled ||
		!cfg.TikTokProfileRevisitJumpEnabled {
		t.Fatal("TikTok 添加桌面和侧边栏入口默认应开启")
	}
	if cfg.TikTok.OAuthTimeout != 5*time.Second {
		t.Fatalf("TikTok OAuth 超时 = %v，期望 5s", cfg.TikTok.OAuthTimeout)
	}
	if len(cfg.CORSAllowedOrigins) != 2 {
		t.Fatalf(
			"CORSAllowedOrigins = %#v，期望两个来源",
			cfg.CORSAllowedOrigins,
		)
	}
	if cfg.MySQL.DSN != "user:password@tcp(localhost:3306)/database" {
		t.Fatal("MySQL DSN 未正确读取")
	}
}

func TestLoadTikTokLoginRequiresServerCredentials(t *testing.T) {
	t.Setenv("APP_ENV", "test")
	t.Setenv("HTTP_ADDR", "127.0.0.1:8080")
	t.Setenv("CORS_ALLOWED_ORIGINS", "http://localhost:7456")
	t.Setenv("MYSQL_DSN", "user:password@tcp(localhost:3306)/database")
	t.Setenv("ENABLE_TIKTOK_LOGIN", "true")
	t.Setenv("TIKTOK_CLIENT_KEY", "")
	t.Setenv("TIKTOK_CLIENT_SECRET", "")

	_, err := Load()
	if err == nil || !strings.Contains(err.Error(), "TIKTOK_CLIENT_KEY") {
		t.Fatalf("Load() 错误 = %v，期望提示缺少 Client Key", err)
	}
}

func TestLoadAnalyticsConfiguration(t *testing.T) {
	t.Setenv("APP_ENV", "production")
	t.Setenv("HTTP_ADDR", "127.0.0.1:23010")
	t.Setenv("CORS_ALLOWED_ORIGINS", "https://game.example.com")
	t.Setenv("MYSQL_DSN", "user:password@tcp(localhost:3306)/database")
	t.Setenv("ANALYTICS_ENABLED", "true")
	t.Setenv("ANALYTICS_TIMEZONE", "Asia/Shanghai")
	t.Setenv("ANALYTICS_GM_USERS", "goods-admin:strong-password,ops:another-password")
	t.Setenv("ANALYTICS_GM_SESSION_TTL", "24h")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() 返回意外错误: %v", err)
	}
	if !cfg.Analytics.Enabled || len(cfg.Analytics.GMUsers) != 2 ||
		cfg.Analytics.GMUsers["goods-admin"] != "strong-password" ||
		cfg.Analytics.GMUsers["ops"] != "another-password" ||
		cfg.Analytics.SessionTTL != 24*time.Hour ||
		cfg.Analytics.Location.String() != "Asia/Shanghai" {
		t.Fatalf("统计配置不正确: %#v", cfg.Analytics)
	}
}

func TestLoadAnalyticsRejectsInvalidMultipleUsers(t *testing.T) {
	t.Setenv("APP_ENV", "test")
	t.Setenv("HTTP_ADDR", "127.0.0.1:8080")
	t.Setenv("CORS_ALLOWED_ORIGINS", "http://localhost:7456")
	t.Setenv("MYSQL_DSN", "user:password@tcp(localhost:3306)/database")
	t.Setenv("ANALYTICS_ENABLED", "true")
	t.Setenv("ANALYTICS_GM_USERS", "x:test-password")

	_, err := Load()
	if err == nil || !strings.Contains(err.Error(), "ANALYTICS_GM_USERS") {
		t.Fatalf("Load() 错误=%v，期望拒绝非法多账号配置", err)
	}
}

func TestLoadAnalyticsSupportsLegacyCredentials(t *testing.T) {
	t.Setenv("APP_ENV", "test")
	t.Setenv("HTTP_ADDR", "127.0.0.1:8080")
	t.Setenv("CORS_ALLOWED_ORIGINS", "http://localhost:7456")
	t.Setenv("MYSQL_DSN", "user:password@tcp(localhost:3306)/database")
	t.Setenv("ANALYTICS_ENABLED", "true")
	t.Setenv("ANALYTICS_GM_USERS", "")
	t.Setenv("ANALYTICS_GM_ACCOUNT", "goods-admin")
	t.Setenv("ANALYTICS_GM_PASSWORD", "strong-password")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("旧版 GM 凭据兼容加载失败: %v", err)
	}
	if cfg.Analytics.GMUsers["goods-admin"] != "strong-password" {
		t.Fatalf("旧版 GM 凭据未正确加载: %#v", cfg.Analytics.GMUsers)
	}
}

func TestLoadTikTokLoginConfiguration(t *testing.T) {
	t.Setenv("APP_ENV", "production")
	t.Setenv("HTTP_ADDR", "127.0.0.1:23010")
	t.Setenv("CORS_ALLOWED_ORIGINS", "https://game.example.com")
	t.Setenv("MYSQL_DSN", "user:password@tcp(localhost:3306)/database")
	t.Setenv("ENABLE_TIKTOK_LOGIN", "true")
	t.Setenv("TIKTOK_CLIENT_KEY", "client-key")
	t.Setenv("TIKTOK_CLIENT_SECRET", "client-secret")
	t.Setenv("TIKTOK_OAUTH_TIMEOUT", "8s")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() 返回意外错误: %v", err)
	}
	if !cfg.EnableTikTokLogin || cfg.TikTok.ClientKey != "client-key" ||
		cfg.TikTok.ClientSecret != "client-secret" ||
		cfg.TikTok.OAuthTimeout != 8*time.Second {
		t.Fatalf("TikTok 配置 = %#v", cfg.TikTok)
	}
}

func TestLoadAllowsExplicitProductionTestAccountLogin(t *testing.T) {
	t.Setenv("APP_ENV", "production")
	t.Setenv("HTTP_ADDR", "127.0.0.1:23010")
	t.Setenv("CORS_ALLOWED_ORIGINS", "https://jptt-aws.bffbond.com")
	t.Setenv("ENABLE_TEST_ACCOUNT_LOGIN", "true")
	t.Setenv("MYSQL_DSN", "user:password@tcp(localhost:3306)/database")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() 返回了意外错误: %v", err)
	}
	if !cfg.EnableTestAccount {
		t.Fatal("显式开关未开放 production 测试账号登录")
	}
}

func TestLoadRejectsInvalidTestAccountLoginSwitch(t *testing.T) {
	t.Setenv("APP_ENV", "test")
	t.Setenv("HTTP_ADDR", "127.0.0.1:8080")
	t.Setenv("CORS_ALLOWED_ORIGINS", "http://localhost:7456")
	t.Setenv("ENABLE_TEST_ACCOUNT_LOGIN", "enabled")
	t.Setenv("MYSQL_DSN", "user:password@tcp(localhost:3306)/database")

	_, err := Load()
	if err == nil || !strings.Contains(err.Error(), "ENABLE_TEST_ACCOUNT_LOGIN") {
		t.Fatalf(
			"Load() 错误 = %v，期望提示测试账号登录开关不合法",
			err,
		)
	}
}

func TestLoadUsesDefaultLogLevel(t *testing.T) {
	t.Setenv("APP_ENV", "test")
	t.Setenv("HTTP_ADDR", "127.0.0.1:8080")
	t.Setenv("LOG_LEVEL", "")
	t.Setenv("CORS_ALLOWED_ORIGINS", "http://localhost:7456")
	t.Setenv("MYSQL_DSN", "user:password@tcp(localhost:3306)/database")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() 返回了意外错误: %v", err)
	}

	if cfg.LogLevel != slog.LevelInfo {
		t.Fatalf("LogLevel = %v，期望默认值 %v", cfg.LogLevel, slog.LevelInfo)
	}
}

func TestLoadRejectsMissingRequiredEnvironment(t *testing.T) {
	t.Setenv("APP_ENV", "")
	t.Setenv("HTTP_ADDR", ":8080")
	t.Setenv("CORS_ALLOWED_ORIGINS", "http://localhost:7456")
	t.Setenv("MYSQL_DSN", "user:password@tcp(localhost:3306)/database")

	_, err := Load()
	if err == nil || !strings.Contains(err.Error(), "APP_ENV") {
		t.Fatalf("Load() 错误 = %v，期望提示缺少 APP_ENV", err)
	}
}

func TestLoadRejectsInvalidValues(t *testing.T) {
	tests := []struct {
		name     string
		appEnv   string
		httpAddr string
		logLevel string
		wantText string
	}{
		{
			name:     "非法运行环境",
			appEnv:   "local",
			httpAddr: ":8080",
			logLevel: "info",
			wantText: "APP_ENV",
		},
		{
			name:     "非法监听地址",
			appEnv:   "test",
			httpAddr: "8080",
			logLevel: "info",
			wantText: "HTTP_ADDR",
		},
		{
			name:     "非法日志级别",
			appEnv:   "test",
			httpAddr: ":8080",
			logLevel: "verbose",
			wantText: "LOG_LEVEL",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("APP_ENV", test.appEnv)
			t.Setenv("HTTP_ADDR", test.httpAddr)
			t.Setenv("LOG_LEVEL", test.logLevel)
			t.Setenv("CORS_ALLOWED_ORIGINS", "http://localhost:7456")
			t.Setenv("MYSQL_DSN", "user:password@tcp(localhost:3306)/database")

			_, err := Load()
			if err == nil || !strings.Contains(err.Error(), test.wantText) {
				t.Fatalf("Load() 错误 = %v，期望包含 %q", err, test.wantText)
			}
		})
	}
}

func TestLoadRejectsUnsafeCORSOrigins(t *testing.T) {
	tests := []struct {
		name    string
		appEnv  string
		origins string
	}{
		{
			name:    "拒绝通配符",
			appEnv:  "development",
			origins: "*",
		},
		{
			name:    "拒绝带路径的来源",
			appEnv:  "development",
			origins: "http://localhost:7456/path",
		},
		{
			name:    "生产环境拒绝 HTTP",
			appEnv:  "production",
			origins: "http://game.example.com",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("APP_ENV", test.appEnv)
			t.Setenv("HTTP_ADDR", ":8080")
			t.Setenv("CORS_ALLOWED_ORIGINS", test.origins)
			t.Setenv(
				"MYSQL_DSN",
				"user:password@tcp(localhost:3306)/database",
			)

			_, err := Load()
			if err == nil ||
				!strings.Contains(err.Error(), "CORS") &&
					!strings.Contains(err.Error(), "生产环境") {
				t.Fatalf("Load() 错误 = %v，期望 CORS 配置错误", err)
			}
		})
	}
}

func TestLoadMySQLConfigUsesPoolDefaults(t *testing.T) {
	t.Setenv("MYSQL_DSN", "user:password@tcp(localhost:3306)/database")
	t.Setenv("MYSQL_MAX_OPEN_CONNS", "")
	t.Setenv("MYSQL_MAX_IDLE_CONNS", "")
	t.Setenv("MYSQL_CONN_MAX_LIFETIME", "")
	t.Setenv("MYSQL_CONN_MAX_IDLE_TIME", "")

	cfg, err := LoadMySQLConfig()
	if err != nil {
		t.Fatalf("LoadMySQLConfig() 返回了意外错误: %v", err)
	}

	if cfg.MaxOpenConns != 20 || cfg.MaxIdleConns != 10 {
		t.Fatalf(
			"连接池默认值 = (%d, %d)，期望 (20, 10)",
			cfg.MaxOpenConns,
			cfg.MaxIdleConns,
		)
	}
}

func TestLoadMySQLConfigRejectsInvalidPool(t *testing.T) {
	t.Setenv("MYSQL_DSN", "user:password@tcp(localhost:3306)/database")
	t.Setenv("MYSQL_MAX_OPEN_CONNS", "5")
	t.Setenv("MYSQL_MAX_IDLE_CONNS", "6")

	_, err := LoadMySQLConfig()
	if err == nil || !strings.Contains(err.Error(), "MYSQL_MAX_IDLE_CONNS") {
		t.Fatalf("LoadMySQLConfig() 错误 = %v，期望提示空闲连接数过大", err)
	}
}
