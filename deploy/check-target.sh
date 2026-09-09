#!/bin/sh
# 在 migration 前运行：比较实际环境与本次发布清单，不输出秘密。
set -eu
[ "${BT_PROJECT_KEY:-}" = 'pairpop-bt' ] || { echo 'BT 远端 BT_PROJECT_KEY 与发布清单不一致' >&2; exit 2; }
[ "${BT_RUNTIME:-}" = 'tiktok-native' ] || { echo 'BT 远端 BT_RUNTIME 与发布清单不一致' >&2; exit 2; }
[ "${BT_ENV:-}" = 'staging' ] || { echo 'BT 远端 BT_ENV 与发布清单不一致' >&2; exit 2; }
[ "${APP_ENV:-}" = 'staging' ] || { echo 'BT 远端 APP_ENV 与发布清单不一致' >&2; exit 2; }
[ "${HTTP_ADDR:-}" = '0.0.0.0:23001' ] || { echo 'BT 远端 HTTP_ADDR 与发布清单不一致' >&2; exit 2; }
[ "${BT_DB_HOST:-}" = 'database-2.c9ie8y6eckdj.ap-northeast-1.rds.amazonaws.com' ] || { echo 'BT 远端 BT_DB_HOST 与发布清单不一致' >&2; exit 2; }
[ "${BT_DB_PORT:-}" = '3306' ] || { echo 'BT 远端 BT_DB_PORT 与发布清单不一致' >&2; exit 2; }
[ "${BT_DB_NAME:-}" = 'linkgamebt' ] || { echo 'BT 远端 BT_DB_NAME 与发布清单不一致' >&2; exit 2; }
[ "${BT_DB_USER:-}" = 'linkgamebt' ] || { echo 'BT 远端 BT_DB_USER 与发布清单不一致' >&2; exit 2; }
[ "${TIKTOK_CLIENT_KEY:-}" = 'mgtbarscemwda9wa' ] || { echo 'BT 远端 TIKTOK_CLIENT_KEY 与发布清单不一致' >&2; exit 2; }
[ "${ENABLE_TIKTOK_LOGIN:-}" = 'true' ] || { echo 'BT 远端 ENABLE_TIKTOK_LOGIN 与发布清单不一致' >&2; exit 2; }
[ "${ENABLE_TEST_ACCOUNT_LOGIN:-}" = 'true' ] || { echo 'BT 远端 ENABLE_TEST_ACCOUNT_LOGIN 与发布清单不一致' >&2; exit 2; }
[ "${BT_ALLOW_WEB_PREVIEW:-}" = 'true' ] || { echo 'BT 远端 BT_ALLOW_WEB_PREVIEW 与发布清单不一致' >&2; exit 2; }
[ "${ANALYTICS_ENABLED:-}" = 'true' ] || { echo 'BT 远端 ANALYTICS_ENABLED 与发布清单不一致' >&2; exit 2; }
[ "${ANALYTICS_TIMEZONE:-}" = 'Asia/Tokyo' ] || { echo 'BT 远端 ANALYTICS_TIMEZONE 与发布清单不一致' >&2; exit 2; }
