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
	"github.com/ben/ikite-go/internal/sources/ims"
	"github.com/ben/ikite-go/internal/sources/openmeteo"
	"github.com/ben/ikite-go/internal/sources/openskiron"
	"github.com/ben/ikite-go/internal/store"
)

func main() {
	force := flag.Bool("force", false, "run now (ignore 9am schedule)")
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
	svc := &collector.ExtForecastService{
		Cfg:        cfg,
		Store:      st,
		OpenMeteo:  openmeteo.New(),
		OpenSkiron: openskiron.New(),
		IMS:        ims.New(cfg.IMSAPIToken),
		Log:        log,
		Notify: &collector.ForecastGustNotifyService{
			Cfg:      cfg,
			Store:    st,
			Telegram: telegram.New(cfg.TelegramAlertToken, cfg.TelegramAlertChatID),
			Log:      log,
		},
	}
	if err := svc.Run(time.Now(), collector.ExtForecastOptions{Force: *force}); err != nil {
		log.Error("ext forecast failed", "err", err)
		os.Exit(1)
	}
}
