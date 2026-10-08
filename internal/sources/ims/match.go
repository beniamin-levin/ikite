package ims

import (
	"math"
	"time"

	"github.com/ben/ikite-go/internal/models"
)

const earthRadiusKm = 6371.0

// HaversineKm returns the great-circle distance between two WGS84 points in kilometres.
func HaversineKm(lat1, lon1, lat2, lon2 float64) float64 {
	rad := math.Pi / 180
	dLat := (lat2 - lat1) * rad
	dLon := (lon2 - lon1) * rad
	a := math.Sin(dLat/2)*math.Sin(dLat/2) +
		math.Cos(lat1*rad)*math.Cos(lat2*rad)*math.Sin(dLon/2)*math.Sin(dLon/2)
	return 2 * earthRadiusKm * math.Atan2(math.Sqrt(a), math.Sqrt(1-a))
}

// NearestStation returns the closest station within maxKm, or nil if none qualify.
func NearestStation(stations []Station, lat, lon, maxKm float64) (*Station, float64) {
	var best *Station
	bestDist := maxKm + 1
	for i := range stations {
		st := &stations[i]
		if !st.HasCoords() {
			continue
		}
		d := HaversineKm(lat, lon, st.Lat, st.Lon)
		if d <= maxKm && d < bestDist {
			best = st
			bestDist = d
		}
	}
	if best == nil {
		return nil, 0
	}
	return best, bestDist
}

// FilterReadingsSince keeps readings at or after since.
func FilterReadingsSince(rows []models.WindReading, since time.Time) []models.WindReading {
	out := make([]models.WindReading, 0, len(rows))
	for _, r := range rows {
		if !r.Period.Before(since) {
			out = append(out, r)
		}
	}
	return out
}
