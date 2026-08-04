package config

import (
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/joho/godotenv"
)

type Config struct {
	HTTPAddr string
	DSN      string
	Timezone *time.Location

	KYHistoryURL      string
	SurfoLiveURL      string
	WindometerLiveURL string
	BegetProxyURL     string
	BegetProxySecret  string
	OpenWRFPDFURL     string

	TelegramAlertToken  string
	TelegramAlertChatID string
	TelegramAIToken     string
	TelegramAIChatID    string

	AlertStartHour int
	AlertEndHour   int
	KYCollectStart int
	KYCollectEnd   int

	WGTimerScript   string
	WGTimerQueueDir string
	SettingsPass    string
}

func Load() (*Config, error) {
	_ = godotenv.Load()

	tzName := getenv("TZ", "Asia/Jerusalem")
	loc, err := time.LoadLocation(tzName)
	if err != nil {
		return nil, fmt.Errorf("load timezone %q: %w", tzName, err)
	}

	dsn := os.Getenv("DB_DSN")
	if dsn == "" {
		host := getenv("DB_HOST", "127.0.0.1")
		port := getenv("DB_PORT", "3306")
		user := getenv("DB_USER", "ikite")
		pass := os.Getenv("DB_PASSWORD")
		name := getenv("DB_NAME", "ikite")
		dsn = fmt.Sprintf("%s:%s@tcp(%s:%s)/%s?parseTime=true&loc=%s&charset=utf8mb4",
			user, pass, host, port, name, "Asia%2FJerusalem")
	}

	return &Config{
		HTTPAddr:            getenv("HTTP_ADDR", ":8080"),
		DSN:                 dsn,
		Timezone:            loc,
		KYHistoryURL:        os.Getenv("KY_HISTORY_URL"),
		SurfoLiveURL:        os.Getenv("SURFO_LIVE_URL"),
		WindometerLiveURL:   os.Getenv("WINDOMETER_LIVE_URL"),
		BegetProxyURL:       os.Getenv("BEGET_PROXY_URL"),
		BegetProxySecret:    os.Getenv("BEGET_PROXY_SECRET"),
		OpenWRFPDFURL:       os.Getenv("OPENWRF_PDF_URL"),
		TelegramAlertToken:  os.Getenv("TELEGRAM_ALERT_TOKEN"),
		TelegramAlertChatID: os.Getenv("TELEGRAM_ALERT_CHAT_ID"),
		TelegramAIToken:     os.Getenv("TELEGRAM_AI_TOKEN"),
		TelegramAIChatID:    os.Getenv("TELEGRAM_AI_CHAT_ID"),
		AlertStartHour:      getenvInt("ALERT_START_HOUR", 8),
		AlertEndHour:        getenvInt("ALERT_END_HOUR", 17),
		KYCollectStart:      getenvInt("KY_COLLECT_START_HOUR", 9),
		KYCollectEnd:        getenvInt("KY_COLLECT_END_HOUR", 19),
		WGTimerScript:       os.Getenv("WG_TIMER_SCRIPT"),
		WGTimerQueueDir:     os.Getenv("WG_TIMER_QUEUE_DIR"),
		SettingsPass:        os.Getenv("SETTINGS_PASS"),
	}, nil
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func getenvInt(key string, fallback int) int {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return fallback
	}
	return n
}
