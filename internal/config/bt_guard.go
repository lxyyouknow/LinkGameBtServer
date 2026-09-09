package config

import (
	"errors"
	"net"
	"os"
	"slices"
	"strings"

	mysqldriver "github.com/go-sql-driver/mysql"
)

// ValidateBTEnvironment 在 API 和 migration 的任何数据库连接之前执行。
// 只返回字段名，不打印 DSN、密码或平台凭据。新环境未提供时必须失败关闭。
func ValidateBTEnvironment() error {
	return validateBTEnvironment(os.Getenv)
}

func validateBTEnvironment(getenv func(string) string) error {
	if getenv("BT_PROJECT_KEY") != "pairpop-bt" {
		return errors.New("BT_PROJECT_KEY 必须为 pairpop-bt；禁止使用旧项目配置")
	}
	env := getenv("BT_ENV")
	if !slices.Contains(validAppEnvironments, env) || env != getenv("APP_ENV") {
		return errors.New("BT_ENV 与 APP_ENV 必须是相同的新项目环境")
	}
	dsn, err := mysqldriver.ParseDSN(getenv("MYSQL_DSN"))
	if err != nil || getenv("MYSQL_DSN") == "" {
		return errors.New("MYSQL_DSN 缺失或格式不合法；请填新 BT 数据库")
	}
	host, port, err := net.SplitHostPort(dsn.Addr)
	if err != nil || dsn.Net != "tcp" {
		return errors.New("BT 数据库必须使用明确的 TCP 地址")
	}
	if host == "" || port == "" || host != getenv("BT_DB_HOST") || port != getenv("BT_DB_PORT") {
		return errors.New("MYSQL_DSN 地址与 BT_DB_HOST/BT_DB_PORT 不一致")
	}
	// 2026-09-09 运维实际分配的独立库；精确匹配实例、端口、库和账号。
	assigned := host == "database-2.c9ie8y6eckdj.ap-northeast-1.rds.amazonaws.com" && port == "3306" && dsn.DBName == "linkgamebt" && dsn.User == "linkgamebt"
	if dsn.DBName != getenv("BT_DB_NAME") || (!assigned && !strings.HasPrefix(dsn.DBName, "linkgame_bt_")) {
		return errors.New("必须使用独立的 linkgame_bt_ 前缀数据库，并与 BT_DB_NAME 一致")
	}
	if dsn.User != getenv("BT_DB_USER") || (!assigned && !strings.HasPrefix(dsn.User, "linkgame_bt_")) {
		return errors.New("必须使用独立的 linkgame_bt_ 前缀数据库用户，并与 BT_DB_USER 一致")
	}
	if (env == "development" || env == "test") && host != "127.0.0.1" && host != "localhost" && host != "::1" {
		return errors.New("BT 本地开发/测试只允许连接本机数据库")
	}
	if env == "staging" || env == "production" {
		// Web 测试是显式部署模式，不是 TikTok 登录失败后的兜底。
		if getenv("BT_RUNTIME") == "web-preview" {
			if env != "staging" || getenv("ENABLE_TEST_ACCOUNT_LOGIN") != "true" || getenv("ENABLE_TIKTOK_LOGIN") != "false" {
				return errors.New("Web 测试仅允许 staging，且须明确启用测试账号、禁用 TikTok 登录")
			}
			return nil
		}
		if getenv("BT_RUNTIME") != "" && getenv("BT_RUNTIME") != "tiktok-native" {
			return errors.New("未知 BT_RUNTIME")
		}
		allowWeb := env == "staging" && getenv("BT_ALLOW_WEB_PREVIEW") == "true"
		if (getenv("ENABLE_TEST_ACCOUNT_LOGIN") != "false" && !(allowWeb && getenv("ENABLE_TEST_ACCOUNT_LOGIN") == "true")) || getenv("ENABLE_TIKTOK_LOGIN") != "true" {
			return errors.New("BT 云环境只允许 TikTok 登录，必须关闭免密码测试账号")
		}
		if getenv("TIKTOK_CLIENT_KEY") == "" || getenv("TIKTOK_CLIENT_SECRET") == "" {
			return errors.New("缺少新 TikTok 应用的服务端配置")
		}
	}
	return nil
}
