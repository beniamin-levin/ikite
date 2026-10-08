package main

import (
	"flag"
	"log/slog"
	"os"
	"time"

	"github.com/ben/ikite-go/internal/config"
	"github.com/ben/ikite-go/internal/db"
	"github.com/ben/ikite-go/internal/notify/telegram"
	"github.com/ben/ikite-go/internal/squall"
	"github.com/ben/ikite-go/internal/store"
	"github.com/ben/ikite-go/internal/translate"
)

// squall runs one pass of the Kiryat Haim squall alerts: new radar frames,
// upwind stations, storms measured at the spot, and post-storm tuning. The
// systemd timer runs it every minute.
func main() {
	backfillFrom := flag.String("backfill-lightning-from", "", "store lightning frames from this local time (2006-01-02T15:04) instead of a normal run")
	backfillTo := flag.String("backfill-lightning-to", "", "…until this local time")
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

	tg := telegram.New(cfg.TelegramStormToken, cfg.TelegramStormChatID)
	if !tg.Enabled() {
		log.Warn("squall: TELEGRAM_STORM_TOKEN / TELEGRAM_STORM_CHAT_ID not set; alerts are recorded but not sent")
	}
	svc := &squall.Service{
		Store:      store.New(sqlDB),
		Log:        log,
		Radar:      squall.NewClient(),
		TG:         tg,
		ArchiveDir: cfg.SquallArchiveDir,
		TZ:         cfg.Timezone,
		Target:     squall.KiryatHaim,
		Translate:  translate.New(),
	}
	if *backfillFrom != "" {
		from, err1 := time.ParseInLocation("2006-01-02T15:04", *backfillFrom, cfg.Timezone)
		to, err2 := time.ParseInLocation("2006-01-02T15:04", *backfillTo, cfg.Timezone)
		if err1 != nil || err2 != nil || to.Before(from) {
			log.Error("backfill: bad -backfill-lightning-from/-to")
			os.Exit(2)
		}
		n, err := svc.BackfillLightning(from, to, time.Now().In(cfg.Timezone))
		log.Info("lightning backfill", "frames", n, "err", err)
		if err != nil {
			os.Exit(1)
		}
		return
	}
	if err := svc.Run(time.Now()); err != nil {
		log.Error("squall run", "err", err)
		os.Exit(1)
	}
}
