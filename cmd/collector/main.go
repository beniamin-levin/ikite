package main

import (
	"flag"
	"log/slog"
	"os"
	"time"

	"github.com/ben/ikite-go/internal/begetproxy"
	"github.com/ben/ikite-go/internal/collector"
	"github.com/ben/ikite-go/internal/config"
	"github.com/ben/ikite-go/internal/db"
	"github.com/ben/ikite-go/internal/notify/telegram"
	"github.com/ben/ikite-go/internal/sources/kyhistory"
	"github.com/ben/ikite-go/internal/sources/windguru"
	"github.com/ben/ikite-go/internal/sources/windometer"
	"github.com/ben/ikite-go/internal/store"
)

func main() {
	wgStation := flag.Int("wg-station", 0, "fetch and save a single Windguru station ID")
	probeKY := flag.Bool("probe-ky", false, "fetch both surfo KY paths and print their tips, without saving")
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

	proxy := begetproxy.New(cfg.BegetProxyURL, cfg.BegetProxySecret)

	ky := kyhistory.New(proxy, cfg.KYHistoryURL)
	ky.Log = log

	svc := &collector.Service{
		Cfg:      cfg,
		Store:    store.New(sqlDB),
		WG:       windguru.New(proxy),
		KY:       ky,
		KH:       windometer.New(proxy, cfg.WindometerLiveURL),
		Telegram: telegram.New(cfg.TelegramAlertToken, cfg.TelegramAlertChatID),
		Log:      log,
	}

	now := time.Now().In(cfg.Timezone)

	// -probe-ky exercises the JSON feed and the HTML fallback side by side. The
	// fallback only fires when the JSON is stale, so this is the only way to see
	// it work while the feed is healthy.
	if *probeKY {
		rows, stats, err := ky.Fetch(now)
		if err != nil {
			log.Error("probe ky json", "err", err)
		} else {
			log.Info("probe ky json", "rows", len(rows),
				"tip", rows[len(rows)-1].Period, "msg", stats.Msg)
		}
		table, err := ky.FetchWindtable(now)
		if err != nil {
			log.Error("probe ky windtable", "err", err)
			os.Exit(1)
		}
		log.Info("probe ky windtable", "rows", len(table),
			"tip", table[len(table)-1].Period,
			"wind", table[len(table)-1].Wind, "gust", table[len(table)-1].Gust,
			"dir", table[len(table)-1].WindDir)
		return
	}

	if *wgStation > 0 {
		if err := svc.RunWindguruStation(now, *wgStation); err != nil {
			log.Error("windguru station failed", "station", *wgStation, "err", err)
			os.Exit(1)
		}
		log.Info("windguru station done", "station", *wgStation)
		return
	}

	res, err := svc.Run(now)
	if err != nil {
		log.Error("collector failed", "err", err)
		os.Exit(1)
	}
	log.Info("collector done",
		"saved", res.SavedCount,
		"wind_ky", res.WindKY,
		"wind_kh", res.WindKH,
		"alert", res.AlertSent,
	)
}
