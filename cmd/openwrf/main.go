package main

import (
	"flag"
	"log/slog"
	"os"
	"time"

	"github.com/ben/ikite-go/internal/collector"
	"github.com/ben/ikite-go/internal/config"
	"github.com/ben/ikite-go/internal/db"
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

	svc := &collector.OpenWRFForecastService{
		Cfg:     cfg,
		Store:   store.New(sqlDB),
		OpenWRF: openwrf.New(cfg.OpenWRFPDFURL),
		Log:     log,
	}
	if err := svc.Run(time.Now(), collector.OpenWRFForecastOptions{Force: *force}); err != nil {
		log.Error("openWRF forecast failed", "err", err)
		os.Exit(1)
	}
}
