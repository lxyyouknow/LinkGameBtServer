package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"time"

	"linkgame-server/internal/config"
	mysqlstore "linkgame-server/internal/store/mysql"
	"linkgame-server/internal/store/mysql/migrations"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	migrationsPath := flag.String(
		"path",
		"migrations",
		"数据库迁移文件所在目录",
	)
	flag.Parse()
	if err := config.ValidateBTEnvironment(); err != nil {
		return err
	}

	mysqlConfig, err := config.LoadMySQLConfig()
	if err != nil {
		return fmt.Errorf("加载数据库配置失败: %w", err)
	}

	connectContext, cancelConnect := context.WithTimeout(
		context.Background(),
		10*time.Second,
	)
	defer cancelConnect()

	db, err := mysqlstore.Open(connectContext, mysqlConfig)
	if err != nil {
		return err
	}
	defer func() {
		_ = db.Close()
	}()

	if err := migrations.Up(db, *migrationsPath); err != nil {
		return err
	}

	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	logger.Info("数据库迁移完成", "migrations_path", *migrationsPath)
	return nil
}
