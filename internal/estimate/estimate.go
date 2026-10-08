// Package estimate turns several models' wind forecasts for a spot into one best
// estimate, calibrated against what that spot's meter actually measured.
//
// Method: each model's forecast is shifted by its own average error at the spot
// for that time of day (bias correction), then the models are averaged with
// weights inversely proportional to how much they still miss after that
// correction. Gusts get the same treatment independently. The likely range combines the estimate's own
// historical error with how far the corrected models disagree at that hour, so
// it widens further out, where the models drift apart.
//
// Chosen by leave-one-day-out backtest over 11 spots and ~4,700 hours (Sep 2026):
//
//	plain average of all models        MAE 2.57 kt, bias +1.40
//	the single best model              MAE 2.10 kt
//	one bias per model for the day     MAE 1.74 kt, bias +0.02
//	linear-regression calibration      MAE 1.61 kt — but it under-reads strong
//	                                   days most (−3.7 kt on ≥15 kt hours vs −2.7
//	                                   for bias correction), the hours that matter
//	bias per model per time of day     MAE 1.54 kt, 81% inside the range (this)
//
// Gusts get the same time-of-day treatment: 2.47 → 2.21 kt error held-out.
//
// Time of day matters because at a sea-breeze spot a model's error is not the
// same at 07:00 and 14:00: at Kiryat Yam, splitting the day cut the error from
// 2.53 to 1.87 kt.
//
// For gusts, the highest model gust — what a reader naturally looks at — was off
// by +4.1 kt on average; the corrected, weighted gust was off by 0.0.
package estimate

import (
	"math"
	"sort"
)

const (
	// MinHours is the history a model needs before it is trusted in the mix.
	MinHours = 24
	// RangeZ scales the likely range. Tuned in the backtest so that about 80% of
	// measured hours fall inside it (1.0 gave 76%, 1.2 gave 85%).
	RangeZ = 1.1
	// varFloor keeps one lucky model from taking all the weight: nobody forecasts
	// a spot to better than about ±0.5 kt.
	varFloor = 0.25
	// minHalfRange: a range narrower than ±1 kt would claim more certainty than a
	// meter reading itself carries.
	minHalfRange = 1.0
)

// Time-of-day blocks for bias correction: morning, sea-breeze peak, evening.
const numBlocks = 3

func blockOf(hour int) int {
	switch {
	case hour <= 10:
		return 0
	case hour <= 16:
		return 1
	}
	return 2
}

// Forecast is one model's forecast for one hour.
type Forecast struct {
	Model string
	Wind  float64
	Gust  float64 // 0 when the model has none
	Dir   float64 // degrees the wind blows FROM; negative when unknown
}

// Past pairs the forecasts that were issued for an hour with what the meter saw.
type Past struct {
	Hour      int // wall-clock hour of day, 0–23
	Forecasts []Forecast
	Wind      float64 // measured mean wind over the hour
	GustPeak  float64 // highest measured gust in the hour; 0 when unknown
}

// BlockCal is a model's error for one time-of-day block.
type BlockCal struct {
	Bias, Var         float64
	OK                bool // enough hours in this block to trust it
	GustBias, GustVar float64
	GustOK            bool
}

// ModelCal is one model's track record at a spot.
type ModelCal struct {
	Hours int
	Bias  float64 // mean(forecast − measured), whole day
	Var   float64 // variance of the error once the bias is removed
	// Block refines Bias and Var by time of day where there is enough history.
	Block    [numBlocks]BlockCal
	GustBias float64
	GustVar  float64
	HasGust  bool
}

// Calibration is everything needed to turn fresh forecasts into an estimate.
type Calibration struct {
	Models map[string]ModelCal
	// Sigma is the estimate's own RMS error over the history it was fitted on.
	Sigma float64
}

// Usable reports whether there is enough to blend: one model is not a blend.
func (c Calibration) Usable() bool { return len(c.Models) >= 2 }

// Point is the estimate for one hour.
type Point struct {
	Wind, Gust float64
	Low, High  float64 // likely range for the wind
	Dir        float64 // FROM, degrees; negative when no model gave one
	Models     int     // how many calibrated models contributed
}

// Fit learns each model's bias and reliability from past hours.
func Fit(history []Past) Calibration {
	type acc struct {
		n, ng                int
		sum, sumSq, gs, gsSq float64
		bn                   [numBlocks]int
		bs, bss              [numBlocks]float64
		bgn                  [numBlocks]int
		bgs, bgss            [numBlocks]float64
	}
	per := map[string]*acc{}
	for _, h := range history {
		for _, f := range h.Forecasts {
			a := per[f.Model]
			if a == nil {
				a = &acc{}
				per[f.Model] = a
			}
			e := f.Wind - h.Wind
			a.n++
			a.sum += e
			a.sumSq += e * e
			b := blockOf(h.Hour)
			a.bn[b]++
			a.bs[b] += e
			a.bss[b] += e * e
			if f.Gust > 0 && h.GustPeak > 0 {
				ge := f.Gust - h.GustPeak
				a.ng++
				a.gs += ge
				a.gsSq += ge * ge
				a.bgn[b]++
				a.bgs[b] += ge
				a.bgss[b] += ge * ge
			}
		}
	}

	cal := Calibration{Models: map[string]ModelCal{}}
	for name, a := range per {
		if a.n < MinHours {
			continue
		}
		mean := a.sum / float64(a.n)
		mc := ModelCal{
			Hours: a.n,
			Bias:  mean,
			// Variance about the bias: what remains after correcting for it.
			Var: math.Max(a.sumSq/float64(a.n)-mean*mean, 0) + varFloor,
		}
		// Blocks without enough history fall back to the whole-day figures.
		for b := 0; b < numBlocks; b++ {
			if a.bn[b] >= MinHours {
				bm := a.bs[b] / float64(a.bn[b])
				mc.Block[b].Bias = bm
				mc.Block[b].Var = math.Max(a.bss[b]/float64(a.bn[b])-bm*bm, 0) + varFloor
				mc.Block[b].OK = true
			}
			if a.bgn[b] >= MinHours {
				gm := a.bgs[b] / float64(a.bgn[b])
				mc.Block[b].GustBias = gm
				mc.Block[b].GustVar = math.Max(a.bgss[b]/float64(a.bgn[b])-gm*gm, 0) + varFloor
				mc.Block[b].GustOK = true
			}
		}
		if a.ng >= MinHours {
			gm := a.gs / float64(a.ng)
			mc.GustBias = gm
			mc.GustVar = math.Max(a.gsSq/float64(a.ng)-gm*gm, 0) + varFloor
			mc.HasGust = true
		}
		cal.Models[name] = mc
	}
	if !cal.Usable() {
		return cal
	}

	// How far the blended estimate itself missed on those same hours. It is
	// in-sample and therefore a little optimistic; RangeZ was tuned on held-out
	// days with exactly this in place, so the two together are calibrated.
	var n int
	var ss float64
	for _, h := range history {
		if p, ok := cal.blendWind(h.Hour, h.Forecasts); ok {
			d := p - h.Wind
			ss += d * d
			n++
		}
	}
	if n > 0 {
		cal.Sigma = math.Sqrt(ss / float64(n))
	}
	return cal
}

// at returns the model's bias and error variance for an hour of the day.
func (m ModelCal) at(hour int) (bias, variance float64) {
	if b := m.Block[blockOf(hour)]; b.OK {
		return b.Bias, b.Var
	}
	return m.Bias, m.Var
}

// gustAt is at() for gusts. ok is false when the model has no gust history.
func (m ModelCal) gustAt(hour int) (bias, variance float64, ok bool) {
	if b := m.Block[blockOf(hour)]; b.GustOK {
		return b.GustBias, b.GustVar, true
	}
	return m.GustBias, m.GustVar, m.HasGust
}

// blendWind is the bias-corrected, inverse-variance weighted wind.
func (c Calibration) blendWind(hour int, fs []Forecast) (float64, bool) {
	var num, den float64
	for _, f := range fs {
		m, ok := c.Models[f.Model]
		if !ok {
			continue
		}
		bias, variance := m.at(hour)
		w := 1 / variance
		num += w * math.Max(f.Wind-bias, 0)
		den += w
	}
	if den == 0 {
		return 0, false
	}
	return num / den, true
}

// Estimate blends the forecasts for one hour of the day. ok is false when fewer
// than two calibrated models forecast that hour.
func (c Calibration) Estimate(hour int, fs []Forecast) (Point, bool) {
	type part struct{ v, w float64 }
	var winds []part
	var num, den float64
	var gNum, gDen float64
	var dx, dy, dw float64

	// Deduplicate by model: one model, one vote.
	seen := map[string]bool{}
	for _, f := range fs {
		m, ok := c.Models[f.Model]
		if !ok || seen[f.Model] {
			continue
		}
		seen[f.Model] = true
		bias, variance := m.at(hour)
		w := 1 / variance
		v := math.Max(f.Wind-bias, 0)
		winds = append(winds, part{v, w})
		num += w * v
		den += w
		if gb, gv, ok := m.gustAt(hour); ok && f.Gust > 0 {
			gw := 1 / gv
			gNum += gw * math.Max(f.Gust-gb, 0)
			gDen += gw
		}
		if f.Dir >= 0 {
			r := f.Dir * math.Pi / 180
			dx += w * math.Sin(r)
			dy += w * math.Cos(r)
			dw += w
		}
	}
	if len(winds) < 2 {
		return Point{}, false
	}

	p := Point{Wind: num / den, Dir: -1, Models: len(winds)}

	// Disagreement between the corrected models at this hour.
	var sv float64
	for _, x := range winds {
		sv += x.w * (x.v - p.Wind) * (x.v - p.Wind)
	}
	spread := math.Sqrt(sv / den)
	half := math.Max(minHalfRange, RangeZ*math.Sqrt(c.Sigma*c.Sigma+spread*spread))
	p.Low = math.Max(p.Wind-half, 0)
	p.High = p.Wind + half

	if gDen > 0 {
		p.Gust = gNum / gDen
	}
	// A gust below the wind it rides on is not a gust.
	if p.Gust < p.Wind {
		p.Gust = p.Wind
	}

	if dw > 0 && (dx != 0 || dy != 0) {
		d := math.Atan2(dx/dw, dy/dw) * 180 / math.Pi
		if d < 0 {
			d += 360
		}
		p.Dir = d
	}
	return p, true
}

// ModelNames lists the calibrated models, most reliable first.
func (c Calibration) ModelNames() []string {
	out := make([]string, 0, len(c.Models))
	for n := range c.Models {
		out = append(out, n)
	}
	sort.Slice(out, func(i, j int) bool {
		vi, vj := c.Models[out[i]].Var, c.Models[out[j]].Var
		if vi != vj {
			return vi < vj
		}
		return out[i] < out[j]
	})
	return out
}
