package main

import (
	"flag"
	"log/slog"
	"os"
	"time"

	"github.com/ben/ikite-go/internal/collector"
	"github.com/ben/ikite-go/internal/config"
	"github.com/ben/ikite-go/internal/db"
	"github.com/ben/ikite-go/internal/sources/ims"
	"github.com/ben/ikite-go/internal/store"
)

func main() {
	backfill := flag.Bool("backfill", false, "pull a longer IMS window (36h)")
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

	svc := &collector.IMSService{
		Cfg:   cfg,
		Store: store.New(sqlDB),
		IMS:   ims.New(cfg.IMSAPIToken),
		Log:   log,
	}
	if err := svc.RunOpts(time.Now(), collector.IMSRunOptions{Backfill: *backfill}); err != nil {
		log.Error("ims collector failed", "err", err)
		os.Exit(1)
	}
}
