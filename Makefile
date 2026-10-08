.PHONY: tidy build run-server collect forecast migrate up down

tidy:
	go mod tidy

build:
	go build -o bin/server ./cmd/server
	go build -o bin/collector ./cmd/collector
	go build -o bin/forecast ./cmd/forecast
	go build -o bin/wgforecast ./cmd/wgforecast
	go build -o bin/openwrf ./cmd/openwrf
	go build -o bin/extforecast ./cmd/extforecast
	go build -o bin/forecastgust ./cmd/forecastgust
	go build -o bin/ims ./cmd/ims
	go build -o bin/prediction ./cmd/prediction

run-server:
	MIGRATE=1 go run ./cmd/server

collect:
	go run ./cmd/collector

forecast:
	go run ./cmd/forecast

up:
	docker compose up -d

down:
	docker compose down
