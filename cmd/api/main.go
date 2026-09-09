package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"linkgame-server/internal/adminui"
	"linkgame-server/internal/adreward"
	"linkgame-server/internal/analytics"
	"linkgame-server/internal/auth"
	"linkgame-server/internal/config"
	"linkgame-server/internal/dailychallenge"
	"linkgame-server/internal/dailygift"
	"linkgame-server/internal/gm"
	"linkgame-server/internal/httpapi"
	"linkgame-server/internal/leaderboard"
	"linkgame-server/internal/platform"
	tiktokplatform "linkgame-server/internal/platform/tiktok"
	"linkgame-server/internal/player"
	"linkgame-server/internal/season"
	mysqlstore "linkgame-server/internal/store/mysql"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	if err := config.ValidateBTEnvironment(); err != nil {
		return err
	}
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("加载配置失败: %w", err)
	}
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: cfg.LogLevel}))
	slog.SetDefault(logger)
	connectCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	db, err := mysqlstore.Open(connectCtx, cfg.MySQL)
	cancel()
	if err != nil {
		return err
	}
	defer db.Close()
	authStore := mysqlstore.NewAuthStore(db)
	authService := auth.NewService(authStore, cfg.SessionTTL)
	var platformLogin httpapi.PlatformLoginService
	var profileAuthorizer leaderboard.ProfileAuthorizer
	if cfg.EnableTikTokLogin {
		client := tiktokplatform.NewClient(cfg.TikTok.ClientKey, cfg.TikTok.ClientSecret, cfg.TikTok.OAuthTimeout)
		platformLogin = platform.NewLoginService(map[string]platform.Verifier{"tiktok": client}, authService)
		profileAuthorizer = client
	}
	saveService := player.NewService(mysqlstore.NewSaveStore(db))
	leaderboardService := leaderboard.NewService(mysqlstore.NewLeaderboardStore(db), profileAuthorizer)
	dailyGiftService := dailygift.NewService(mysqlstore.NewDailyGiftStore(db))
	dailyChallengeService := dailychallenge.NewService(mysqlstore.NewDailyChallengeStore(db))
	seasonService := season.NewService(mysqlstore.NewSeasonStore(db))
	adRewardService := adreward.NewService(mysqlstore.NewAdRewardStore(db))
	var analyticsService httpapi.AnalyticsService
	if cfg.Analytics.Enabled {
		analyticsService = analytics.NewService(mysqlstore.NewAnalyticsStore(db), cfg.Analytics.Location)
	}
	gmService := gm.NewService(cfg.Analytics.Enabled, cfg.Analytics.GMUsers, cfg.Analytics.SessionTTL)
	server := &http.Server{Addr: cfg.HTTPAddr, Handler: httpapi.NewHandler(logger, httpapi.Dependencies{ReadinessChecker: db, GuestLoginService: authService, TestAccountLogin: authService, EnableTestAccount: cfg.EnableTestAccount, PlatformLogin: platformLogin, EnableTikTokLogin: cfg.EnableTikTokLogin, TikTokHomeShortcutEnabled: cfg.TikTokHomeShortcutEnabled, TikTokProfileRevisitEnabled: cfg.TikTokProfileRevisitEnabled, TikTokProfileRevisitJumpEnabled: cfg.TikTokProfileRevisitJumpEnabled, SessionAuthenticator: authService, SaveService: saveService, LeaderboardService: leaderboardService, DailyGiftService: dailyGiftService, DailyChallengeService: dailyChallengeService, SeasonService: seasonService, AdRewardService: adRewardService, AnalyticsService: analyticsService, GMService: gmService, AdminUI: adminui.Handler(), CORSAllowedOrigins: cfg.CORSAllowedOrigins}), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 15 * time.Second, IdleTimeout: 60 * time.Second}
	signalCtx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	serverErrors := make(chan error, 1)
	go func() {
		logger.Info("HTTP 服务开始监听", "app_env", cfg.AppEnv, "http_addr", cfg.HTTPAddr)
		serverErrors <- server.ListenAndServe()
	}()
	select {
	case err := <-serverErrors:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return fmt.Errorf("HTTP 服务异常退出: %w", err)
	case <-signalCtx.Done():
		logger.Info("收到退出信号，开始优雅关闭")
	}
	shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancelShutdown()
	if err := server.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("优雅关闭失败: %w", err)
	}
	return nil
}
