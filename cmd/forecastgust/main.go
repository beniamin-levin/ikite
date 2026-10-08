package main

import (
	"log/slog"
	"os"
	"time"

	"github.com/ben/ikite-go/internal/collector"
	"github.com/ben/ikite-go/internal/config"
	"github.com/ben/ikite-go/internal/db"
	"github.com/ben/ikite-go/internal/notify/telegram"
	"github.com/ben/ikite-go/internal/store"
)

func main() {
	log := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	cfg, err := config.Load()
	if err != nil {
		log.Error("config", "err", err)
		os.Exit(1)
	}

	sqlDB, err := db.Open(cfg.DSN)
	if err != nil {
		log.Error("db open", "err", err)
		os.Exit(1)
	}
	defer sqlDB.Close()

	svc := &collector.ForecastGustNotifyService{
		Cfg:      cfg,
		Store:    store.New(sqlDB),
		Telegram: telegram.New(cfg.TelegramAlertToken, cfg.TelegramAlertChatID),
		Log:      log,
	}

	opts := collector.ForecastGustNotifyOptions{
		Force:  os.Getenv("FORCE") == "1",
		DryRun: os.Getenv("DRY_RUN") == "1",
	}
	if err := svc.RunOpts(time.Now(), opts); err != nil {
		log.Error("forecast gust notify failed", "err", err)
		os.Exit(1)
	}
}
