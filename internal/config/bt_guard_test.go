package config

import (
	"strings"
	"testing"
)

func TestBTGuard(t *testing.T) {
	baseline := map[string]string{
		"BT_PROJECT_KEY": "pairpop-bt", "BT_ENV": "development", "APP_ENV": "development",
		"MYSQL_DSN":  "linkgame_bt_app:fixture-password@tcp(127.0.0.1:3318)/linkgame_bt_development",
		"BT_DB_HOST": "127.0.0.1", "BT_DB_PORT": "3318", "BT_DB_NAME": "linkgame_bt_development", "BT_DB_USER": "linkgame_bt_app",
	}
	for _, tc := range []struct {
		name      string
		changes   map[string]string
		wantError bool
	}{
		{"新本地环境", nil, false},
		{"缺配置", map[string]string{"BT_PROJECT_KEY": ""}, true},
		{"环境不一致", map[string]string{"BT_ENV": "production"}, true},
		{"旧数据库", map[string]string{"MYSQL_DSN": "linkgame:fixture-password@tcp(127.0.0.1:3318)/linkgame"}, true},
		{"地址不一致", map[string]string{"BT_DB_PORT": "3306"}, true},
		{"本地连远端", map[string]string{"MYSQL_DSN": "linkgame_bt_app:fixture-password@tcp(db.example.invalid:3318)/linkgame_bt_development", "BT_DB_HOST": "db.example.invalid"}, true},
		{"正式缺平台配置", map[string]string{"BT_ENV": "production", "APP_ENV": "production"}, true},
		{"坏DSN不泄漏", map[string]string{"MYSQL_DSN": "fixture-password@invalid("}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			values := make(map[string]string)
			for key, value := range baseline {
				values[key] = value
			}
			for key, value := range tc.changes {
				values[key] = value
			}
			err := validateBTEnvironment(func(key string) string { return values[key] })
			if (err != nil) != tc.wantError {
				t.Fatalf("错误状态不符合预期：%v", err)
			}
			if err != nil && strings.Contains(err.Error(), "fixture-password") {
				t.Fatal("错误消息泄漏秘密")
			}
		})
	}
}

func TestBTAssignedWebEnvironment(t *testing.T) {
	values := map[string]string{
		"BT_PROJECT_KEY": "pairpop-bt", "BT_RUNTIME": "web-preview", "BT_ENV": "staging", "APP_ENV": "staging",
		"BT_DB_HOST": "database-2.c9ie8y6eckdj.ap-northeast-1.rds.amazonaws.com", "BT_DB_PORT": "3306", "BT_DB_NAME": "linkgamebt", "BT_DB_USER": "linkgamebt",
		"MYSQL_DSN":                 "linkgamebt:fixture-password@tcp(database-2.c9ie8y6eckdj.ap-northeast-1.rds.amazonaws.com:3306)/linkgamebt",
		"ENABLE_TEST_ACCOUNT_LOGIN": "true", "ENABLE_TIKTOK_LOGIN": "false",
	}
	get := func(key string) string { return values[key] }
	if err := validateBTEnvironment(get); err != nil {
		t.Fatal(err)
	}
	for _, change := range []map[string]string{
		{"BT_ENV": "production", "APP_ENV": "production"},
		{"BT_RUNTIME": "tiktok-native"},
		{"ENABLE_TIKTOK_LOGIN": "true"},
		{"MYSQL_DSN": "linkgamebt:fixture-password@tcp(other.example.com:3306)/linkgamebt", "BT_DB_HOST": "other.example.com"},
		{"MYSQL_DSN": "linkgame:fixture-password@tcp(database-2.c9ie8y6eckdj.ap-northeast-1.rds.amazonaws.com:3306)/linkgame", "BT_DB_NAME": "linkgame", "BT_DB_USER": "linkgame"},
	} {
		copyValues := make(map[string]string)
		for k, v := range values {
			copyValues[k] = v
		}
		for k, v := range change {
			copyValues[k] = v
		}
		if err := validateBTEnvironment(func(k string) string { return copyValues[k] }); err == nil {
			t.Fatal("错误环境未被拦截")
		}
	}
}

// 2026-09-09：TikTok 预览与已发布 Web 共用新 staging，身份仍由 provider 隔离。
func TestBTNativeWithExplicitWebPreview(t *testing.T) {
	values := map[string]string{
		"BT_PROJECT_KEY": "pairpop-bt", "BT_RUNTIME": "tiktok-native", "BT_ENV": "staging", "APP_ENV": "staging",
		"BT_DB_HOST": "db.example.com", "BT_DB_PORT": "3306", "BT_DB_NAME": "linkgame_bt_staging", "BT_DB_USER": "linkgame_bt_app",
		"MYSQL_DSN":                 "linkgame_bt_app:fixture-password@tcp(db.example.com:3306)/linkgame_bt_staging",
		"ENABLE_TEST_ACCOUNT_LOGIN": "true", "ENABLE_TIKTOK_LOGIN": "true", "BT_ALLOW_WEB_PREVIEW": "true",
		"TIKTOK_CLIENT_KEY": "fixture-key", "TIKTOK_CLIENT_SECRET": "fixture-secret",
	}
	get := func(k string) string { return values[k] }
	if err := validateBTEnvironment(get); err != nil {
		t.Fatal(err)
	}
	delete(values, "BT_ALLOW_WEB_PREVIEW")
	if validateBTEnvironment(get) == nil {
		t.Fatal("未显式授权的混合模式必须拒绝")
	}
	values["BT_ALLOW_WEB_PREVIEW"] = "true"
	values["APP_ENV"] = "production"
	values["BT_ENV"] = "production"
	if validateBTEnvironment(get) == nil {
		t.Fatal("production 不得开启 Web 测试账号")
	}
}
