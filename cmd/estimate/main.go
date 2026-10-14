package main

import (
	"flag"
	"log/slog"
	"os"
	"time"

	"github.com/ben/ikite-go/internal/collector"
	"github.com/ben/ikite-go/internal/config"
	"github.com/ben/ikite-go/internal/db"
	"github.com/ben/ikite-go/internal/store"
)

func main() {
	backfill := flag.Int("backfill", 0, "also replay this many previous days, each calibrated only on the days before it")
	backtest := flag.Int("backtest", 0, "score the calibration methods over this many past days (nothing is written)")
	nowcast := flag.Bool("nowcast", false, "re-issue the rest of today from the meters (hourly)")
	nowcastBacktest := flag.Int("nowcast-backtest", 0, "score the hourly nowcast settings over this many past days (nothing is written)")
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

	svc := &collector.EstimateService{Cfg: cfg, Store: store.New(sqlDB), Log: log}
	if *nowcastBacktest > 0 {
		if err := svc.NowcastBacktest(os.Stdout, time.Now(), *nowcastBacktest); err != nil {
			log.Error("nowcast backtest failed", "err", err)
			os.Exit(1)
		}
		return
	}
	if *nowcast {
		if err := svc.Nowcast(time.Now()); err != nil {
			log.Error("nowcast failed", "err", err)
			os.Exit(1)
		}
		return
	}
	if *backtest > 0 {
		if err := svc.Backtest(os.Stdout, time.Now(), *backtest); err != nil {
			log.Error("backtest failed", "err", err)
			os.Exit(1)
		}
		return
	}
	if err := svc.Run(time.Now(), *backfill); err != nil {
		log.Error("estimate failed", "err", err)
		os.Exit(1)
	}
}
