package squall

import (
	"math"
	"sort"
)

// Params are the tunable rules. They live in settings ("squall_params") and are
// re-fitted after every storm that reaches the spot (tune.go).
type Params struct {
	// DBZ is the reflectivity a cell needs to count as a squall threat. 35 dBZ
	// is where Universal Blue turns yellow: heavy, usually convective rain.
	DBZ int `json:"dbz"`
	// HitKm: a cell whose track passes within this distance of the spot is a
	// threat. Gust fronts spread out sideways from the rain core.
	HitKm float64 `json:"hit_km"`
	// LeadKm: the wind arrives while the rain core is still this far away —
	// the gust front runs out ahead of the rain.
	LeadKm float64 `json:"lead_km"`
	// TriggerMin: warn once the projected arrival is this close.
	TriggerMin float64 `json:"trigger_min"`
	// SpeedFactor scales the measured motion; >1 when storms have been
	// arriving sooner than projected.
	SpeedFactor float64 `json:"speed_factor"`
	// MinCellPx ignores specks smaller than this (each px ≈ 0.26 km²).
	MinCellPx int `json:"min_cell_px"`
	// LightningKm: warn when lightning is this close on the sea side. Negative
	// switches the lightning rule off; 0 (unset) means the default.
	LightningKm float64 `json:"lightning_km"`
}

// DefaultParams is the starting point before any storm has been replayed.
func DefaultParams() Params {
	return Params{DBZ: 35, HitKm: 8, LeadKm: 4, TriggerMin: 40, SpeedFactor: 1, MinCellPx: 4, LightningKm: 30}
}

// Sane clamps params into a range where the rules still make sense, so a bad
// fit can never switch the alerts off or make them fire on drizzle.
func (p Params) Sane() Params {
	d := DefaultParams()
	if p.DBZ < 25 || p.DBZ > 55 {
		p.DBZ = d.DBZ
	}
	p.HitKm = clamp(p.HitKm, 2, 20, d.HitKm)
	p.LeadKm = clamp(p.LeadKm, 0, 15, d.LeadKm)
	p.TriggerMin = clamp(p.TriggerMin, 20, 90, d.TriggerMin)
	p.SpeedFactor = clamp(p.SpeedFactor, 0.5, 2, d.SpeedFactor)
	if p.MinCellPx < 1 || p.MinCellPx > 50 {
		p.MinCellPx = d.MinCellPx
	}
	switch {
	case p.LightningKm < 0:
		p.LightningKm = -1
	case p.LightningKm == 0 || math.IsNaN(p.LightningKm):
		p.LightningKm = d.LightningKm
	default:
		p.LightningKm = math.Max(5, math.Min(60, p.LightningKm))
	}
	return p
}

func clamp(v, lo, hi, def float64) float64 {
	if v == 0 || math.IsNaN(v) {
		return def
	}
	return math.Max(lo, math.Min(hi, v))
}

const (
	// scanRangeKm: cells further out than this are ignored.
	scanRangeKm = 90
	// motionDBZ: the rain used to measure motion — any real rain, not only cores,
	// so the whole system's drift is tracked.
	motionDBZ = 25
	// maxSpeedKmh bounds the motion search; Levant squall lines run 30–70 km/h.
	maxSpeedKmh = 100
	// minMotionPx: too little rain to measure motion from.
	minMotionPx = 40
	// minOverlap: the best shift must line up at least this share of the rain,
	// or the "motion" is noise (cells forming and dying, not moving).
	minOverlap = 0.25
	// maxETAMin: projections further out than this are not worth acting on.
	maxETAMin = 120
)

// Motion is how the rain field moved between two frames.
type Motion struct {
	OK         bool
	VE, VN     float64 // km per minute, east and north
	SpeedKmh   float64
	HeadingDeg float64 // direction it moves TOWARD
	Overlap    float64
}

// MeasureMotion finds the shift that best lines up prev's rain with cur's.
func MeasureMotion(prev, cur *Grid, geo Geometry, dtMin float64) Motion {
	if prev == nil || cur == nil || dtMin <= 0 {
		return Motion{}
	}
	rangePx := (scanRangeKm + 30) / geo.KmPerPx
	type pt struct{ x, y int }
	var pts []pt
	for _, i := range prev.rainIdx() {
		x, y := int(i)%prev.W, int(i)/prev.W
		if prev.DBZ[i] < motionDBZ || math.Hypot(float64(x)-geo.TX, float64(y)-geo.TY) > rangePx {
			continue
		}
		pts = append(pts, pt{x, y})
	}
	if len(pts) < minMotionPx {
		return Motion{}
	}
	maxShift := int(math.Ceil(maxSpeedKmh / 60 * dtMin / geo.KmPerPx))
	bestScore, bestX, bestY := -1, 0, 0
	for sy := -maxShift; sy <= maxShift; sy++ {
		for sx := -maxShift; sx <= maxShift; sx++ {
			if sx*sx+sy*sy > maxShift*maxShift {
				continue
			}
			score := 0
			for _, p := range pts {
				if cur.At(p.x+sx, p.y+sy) >= motionDBZ {
					score++
				}
			}
			// Prefer the smaller shift on ties: stationary rain stays put.
			if score > bestScore || (score == bestScore && sx*sx+sy*sy < bestX*bestX+bestY*bestY) {
				bestScore, bestX, bestY = score, sx, sy
			}
		}
	}
	overlap := float64(bestScore) / float64(len(pts))
	if overlap < minOverlap {
		return Motion{Overlap: overlap}
	}
	ve := float64(bestX) * geo.KmPerPx / dtMin
	vn := -float64(bestY) * geo.KmPerPx / dtMin
	heading := math.Atan2(ve, vn) * 180 / math.Pi
	if heading < 0 {
		heading += 360
	}
	return Motion{OK: true, VE: ve, VN: vn, SpeedKmh: math.Hypot(ve, vn) * 60, HeadingDeg: heading, Overlap: overlap}
}

// Cell is a connected patch of heavy rain.
type Cell struct {
	Px         int
	MaxDBZ     int
	DistKm     float64 // nearest pixel to the spot
	BearingDeg float64 // from the spot to that pixel
	// Track projection; ETAMin < 0 when the cell is not heading for the spot.
	ETAMin  float64
	PassKm  float64 // closest the track comes
	pixels  []int
	nearIdx int
}

// Scan is everything learned from one frame.
type Scan struct {
	MaxDBZ   int
	StrongPx int
	Cells    []Cell // heaviest threat first, then nearest
	Motion   Motion
	// Threat is the cell expected soonest, if any is heading for the spot.
	Threat *Cell
}

// Analyse finds heavy cells and projects each along the measured motion.
func Analyse(g *Grid, mo Motion, p Params, geo Geometry) Scan {
	p = p.Sane()
	sc := Scan{Motion: mo}
	rangePx := scanRangeKm / geo.KmPerPx
	seen := make([]bool, g.W*g.H)
	for _, i := range g.rainIdx() {
		x, y := int(i)%g.W, int(i)/g.W
		d := int(g.DBZ[i])
		if math.Hypot(float64(x)-geo.TX, float64(y)-geo.TY) > rangePx {
			continue
		}
		if d > sc.MaxDBZ {
			sc.MaxDBZ = d
		}
		if d < p.DBZ || seen[i] {
			continue
		}
		if c := floodCell(g, x, y, p.DBZ, seen, geo); c.Px >= p.MinCellPx {
			sc.StrongPx += c.Px
			project(&c, g, mo, p, geo)
			sc.Cells = append(sc.Cells, c)
		}
	}
	sort.Slice(sc.Cells, func(i, j int) bool { return sc.Cells[i].DistKm < sc.Cells[j].DistKm })
	for i := range sc.Cells {
		c := &sc.Cells[i]
		if c.ETAMin < 0 {
			continue
		}
		if sc.Threat == nil || c.ETAMin < sc.Threat.ETAMin {
			sc.Threat = c
		}
	}
	return sc
}

func floodCell(g *Grid, x0, y0, minDBZ int, seen []bool, geo Geometry) Cell {
	c := Cell{DistKm: math.Inf(1), nearIdx: -1}
	stack := []int{y0*g.W + x0}
	seen[y0*g.W+x0] = true
	for len(stack) > 0 {
		i := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		x, y := i%g.W, i/g.W
		c.Px++
		c.pixels = append(c.pixels, i)
		if d := g.At(x, y); d > c.MaxDBZ {
			c.MaxDBZ = d
		}
		e, n := geo.Km(float64(x), float64(y))
		if dist := math.Hypot(e, n); dist < c.DistKm {
			c.DistKm = dist
			c.nearIdx = i
			b := math.Atan2(e, n) * 180 / math.Pi
			if b < 0 {
				b += 360
			}
			c.BearingDeg = b
		}
		for _, d := range [4][2]int{{1, 0}, {-1, 0}, {0, 1}, {0, -1}} {
			nx, ny := x+d[0], y+d[1]
			if nx < 0 || ny < 0 || nx >= g.W || ny >= g.H {
				continue
			}
			j := ny*g.W + nx
			if !seen[j] && g.At(nx, ny) >= minDBZ {
				seen[j] = true
				stack = append(stack, j)
			}
		}
	}
	return c
}

// project sets ETAMin and PassKm: the earliest time any of the cell's pixels
// comes within LeadKm of the spot, or — if the track only grazes it — the time
// of closest approach when that is within HitKm.
func project(c *Cell, g *Grid, mo Motion, p Params, geo Geometry) {
	c.ETAMin, c.PassKm = -1, c.DistKm
	if c.DistKm <= p.LeadKm {
		c.ETAMin, c.PassKm = 0, c.DistKm
		return
	}
	if !mo.OK {
		return
	}
	ve, vn := mo.VE*p.SpeedFactor, mo.VN*p.SpeedFactor
	a := ve*ve + vn*vn
	if a < 1e-6 {
		return
	}
	best, pass := math.Inf(1), math.Inf(1)
	for _, i := range c.pixels {
		e, n := geo.Km(float64(i%g.W), float64(i/g.W))
		b := 2 * (e*ve + n*vn)
		if b >= 0 {
			continue // moving away
		}
		tc := -b / (2 * a)
		dmin := math.Hypot(e+ve*tc, n+vn*tc)
		if dmin < pass {
			pass = dmin
		}
		cc := e*e + n*n - p.LeadKm*p.LeadKm
		var t float64
		if disc := b*b - 4*a*cc; disc >= 0 {
			t = (-b - math.Sqrt(disc)) / (2 * a)
		} else if dmin <= p.HitKm {
			t = tc
		} else {
			continue
		}
		if t >= 0 && t < best {
			best = t
		}
	}
	if !math.IsInf(pass, 1) {
		c.PassKm = pass
	}
	if best <= maxETAMin {
		c.ETAMin = best
	}
}
