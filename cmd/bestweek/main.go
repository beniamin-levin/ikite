// Command bestweek sends the morning "best kite days" summary to the alert
// Telegram chat, once a day, after the 07:30 estimate. DRY_RUN=1 prints it
// instead; FORCE=1 sends even if today's was already sent.
package main

import (
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/ben/ikite-go/internal/bestweek"
	"github.com/ben/ikite-go/internal/config"
	"github.com/ben/ikite-go/internal/db"
	"github.com/ben/ikite-go/internal/notify/telegram"
	"github.com/ben/ikite-go/internal/store"
)

const (
	days    = 7
	siteURL = "https://ikite.fyi/best"
	sentKey = "best_week_sent_date"
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
	st := store.New(sqlDB)

	now := time.Now().In(cfg.Timezone)
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, cfg.Timezone)
	plan, err := bestweek.Load(st, today, days)
	if err != nil {
		log.Error("best week", "err", err)
		os.Exit(1)
	}
	msg := bestweek.Message(plan, siteURL)

	if os.Getenv("DRY_RUN") == "1" {
		fmt.Println(msg)
		return
	}
	date := today.Format("2006-01-02")
	if sent, _ := st.GetSetting(sentKey); sent == date && os.Getenv("FORCE") != "1" {
		log.Info("best week skipped", "reason", "already sent today")
		return
	}
	tg := telegram.New(cfg.TelegramAlertToken, cfg.TelegramAlertChatID)
	if !tg.Enabled() {
		log.Warn("best week skipped", "reason", "telegram not configured")
		return
	}
	if err := tg.Send(msg); err != nil {
		log.Error("best week send", "err", err)
		os.Exit(1)
	}
	if err := st.SetSetting(sentKey, date); err != nil {
		log.Warn("persist best week sent", "err", err)
	}
	log.Info("best week sent", "days", len(plan))
}
