package main

import (
	"flag"
	"log/slog"
	"os"
	"time"

	"github.com/ben/ikite-go/internal/collector"
	"github.com/ben/ikite-go/internal/config"
	"github.com/ben/ikite-go/internal/db"
	"github.com/ben/ikite-go/internal/notify/telegram"
	"github.com/ben/ikite-go/internal/sources/openwrf"
	"github.com/ben/ikite-go/internal/store"
)

func main() {
	force := flag.Bool("force", false, "run now (ignore 8am schedule and replace today's openWRF data)")
	flag.Parse()

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

	st := store.New(sqlDB)
	notify := &collector.ForecastGustNotifyService{
		Cfg:      cfg,
		Store:    st,
		Telegram: telegram.New(cfg.TelegramAlertToken, cfg.TelegramAlertChatID),
		Log:      log,
	}
	svc := &collector.OpenWRFForecastService{
		Cfg:   cfg,
		Store: st,
		Log:   log,
		Notify: notify,
	}
	openwrfClient, err := openwrf.New(cfg.OpenWRFPDFURL, cfg.OpenWRFDriveURL)
	if err != nil {
		log.Error("openWRF client", "err", err)
		os.Exit(1)
	}
	svc.OpenWRF = openwrfClient
	if err := svc.Run(time.Now(), collector.OpenWRFForecastOptions{Force: *force}); err != nil {
		log.Error("openWRF forecast failed", "err", err)
		os.Exit(1)
	}
}
