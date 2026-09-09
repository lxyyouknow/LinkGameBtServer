// Package migrations 负责按版本顺序执行数据库迁移。
package migrations

import (
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"

	migrate "github.com/golang-migrate/migrate/v4"
	migratemysql "github.com/golang-migrate/migrate/v4/database/mysql"
	_ "github.com/golang-migrate/migrate/v4/source/file"
)

// Up 将数据库升级到 migrationsPath 目录中的最新版本。
func Up(db *sql.DB, migrationsPath string) error {
	absolutePath, err := filepath.Abs(migrationsPath)
	if err != nil {
		return fmt.Errorf("解析迁移目录失败: %w", err)
	}

	databaseDriver, err := migratemysql.WithInstance(
		db,
		&migratemysql.Config{},
	)
	if err != nil {
		return fmt.Errorf("初始化 MySQL 迁移驱动失败: %w", err)
	}

	sourceURL := (&url.URL{
		Scheme: "file",
		Path:   filepath.ToSlash(absolutePath),
	}).String()
	runner, err := migrate.NewWithDatabaseInstance(
		sourceURL,
		"mysql",
		databaseDriver,
	)
	if err != nil {
		return fmt.Errorf("初始化迁移执行器失败: %w", err)
	}
	defer func() {
		_, _ = runner.Close()
	}()

	if err := runner.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("执行数据库迁移失败: %w", err)
	}
	return nil
}
