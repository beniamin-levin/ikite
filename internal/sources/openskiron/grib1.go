package openskiron

import (
	"encoding/binary"
	"fmt"
	"math"
	"sort"
	"time"
)

// PointSample is wind at one forecast time, extracted from GRIB.
type PointSample struct {
	Time   time.Time
	WindMS float64
	GustMS float64
	DirDeg float64
}

type gridMsg struct {
	param   int
	time    time.Time
	ni, nj  int
	lat1    float64
	lon1    float64
	dLat    float64
	dLon    float64
	values  []float64
}

// ExtractPoint samples U/V (and gust when present) at lat/lon from a GRIB1 file.
func ExtractPoint(data []byte, lat, lon float64) ([]PointSample, error) {
	msgs, err := parseGRIB1(data)
	if err != nil {
		return nil, err
	}
	type uvg struct {
		u, v, spd, gust *float64
	}
	byTime := map[int64]*uvg{}
	var times []int64
	for _, m := range msgs {
		val, ok := interpolate(m, lat, lon)
		if !ok {
			continue
		}
		ts := m.time.Unix()
		slot := byTime[ts]
		if slot == nil {
			slot = &uvg{}
			byTime[ts] = slot
			times = append(times, ts)
		}
		v := val
		switch m.param {
		case 33: // U wind
			slot.u = &v
		case 34: // V wind
			slot.v = &v
		case 32: // wind speed (common in minimal openWRF gribs)
			slot.spd = &v
		case 180, 31: // gust (table-dependent)
			slot.gust = &v
		}
	}
	sort.Slice(times, func(i, j int) bool { return times[i] < times[j] })
	out := make([]PointSample, 0, len(times))
	for _, ts := range times {
		slot := byTime[ts]
		var wind, dir float64
		switch {
		case slot.u != nil && slot.v != nil:
			wind = math.Hypot(*slot.u, *slot.v)
			// Meteorological direction: from which the wind blows.
			dir = math.Mod(270-math.Atan2(*slot.v, *slot.u)*180/math.Pi+360, 360)
		case slot.spd != nil:
			wind = *slot.spd
		default:
			continue
		}
		gust := wind
		if slot.gust != nil && *slot.gust > gust {
			gust = *slot.gust
		}
		out = append(out, PointSample{
			Time:   time.Unix(ts, 0).UTC(),
			WindMS: wind,
			GustMS: gust,
			DirDeg: dir,
		})
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no wind samples near %.4f,%.4f", lat, lon)
	}
	return out, nil
}

func parseGRIB1(data []byte) ([]gridMsg, error) {
	var out []gridMsg
	for i := 0; i+8 < len(data); {
		j := findGRIB(data, i)
		if j < 0 {
			break
		}
		if j+8 > len(data) || data[j+7] != 1 {
			i = j + 4
			continue
		}
		totalLen := int(data[j+4])<<16 | int(data[j+5])<<8 | int(data[j+6])
		if totalLen < 32 || j+totalLen > len(data) {
			i = j + 4
			continue
		}
		msg, err := parseGRIB1Message(data[j : j+totalLen])
		if err == nil {
			out = append(out, msg)
		}
		i = j + totalLen
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no GRIB1 messages found")
	}
	return out, nil
}

func findGRIB(data []byte, start int) int {
	for i := start; i+4 <= len(data); i++ {
		if data[i] == 'G' && data[i+1] == 'R' && data[i+2] == 'I' && data[i+3] == 'B' {
			return i
		}
	}
	return -1
}

func parseGRIB1Message(msg []byte) (gridMsg, error) {
	var out gridMsg
	if len(msg) < 40 || msg[7] != 1 {
		return out, fmt.Errorf("not grib1")
	}
	// PDS starts at octet 9 (index 8)
	pdsLen := int(msg[8])
	if pdsLen < 28 || 8+pdsLen > len(msg) {
		return out, fmt.Errorf("bad pds")
	}
	pds := msg[8 : 8+pdsLen]
	out.param = int(pds[8]) // octet 10
	century := int(pds[24])
	if century == 0 {
		century = 20
	}
	year := (century-1)*100 + int(pds[12])
	month := int(pds[13])
	day := int(pds[14])
	hour := int(pds[15])
	minute := int(pds[16])
	out.time = time.Date(year, time.Month(month), day, hour, minute, 0, 0, time.UTC)
	// Forecast time offset
	p1 := int(pds[18])
	timeUnit := int(pds[17])
	out.time = out.time.Add(forecastOffset(timeUnit, p1))

	gdsStart := 8 + pdsLen
	if gdsStart >= len(msg) || int(pds[7]) == 0 {
		return out, fmt.Errorf("no gds")
	}
	gdsLen := int(msg[gdsStart])<<16 | int(msg[gdsStart+1])<<8 | int(msg[gdsStart+2])
	if gdsLen < 32 || gdsStart+gdsLen > len(msg) {
		return out, fmt.Errorf("bad gds")
	}
	gds := msg[gdsStart : gdsStart+gdsLen]
	if gds[5] != 0 { // only Lat/Lon grid
		return out, fmt.Errorf("unsupported grid type %d", gds[5])
	}
	out.ni = int(gds[6])<<8 | int(gds[7])
	out.nj = int(gds[8])<<8 | int(gds[9])
	lat1 := int32(int(gds[10])<<16 | int(gds[11])<<8 | int(gds[12]))
	if lat1&0x800000 != 0 {
		lat1 = lat1 - 0x1000000
	}
	lon1 := int32(int(gds[13])<<16 | int(gds[14])<<8 | int(gds[15]))
	if lon1&0x800000 != 0 {
		lon1 = lon1 - 0x1000000
	}
	lat2 := int32(int(gds[17])<<16 | int(gds[18])<<8 | int(gds[19]))
	if lat2&0x800000 != 0 {
		lat2 = lat2 - 0x1000000
	}
	lon2 := int32(int(gds[20])<<16 | int(gds[21])<<8 | int(gds[22]))
	if lon2&0x800000 != 0 {
		lon2 = lon2 - 0x1000000
	}
	di := int(gds[23])<<8 | int(gds[24])
	dj := int(gds[25])<<8 | int(gds[26])
	out.lat1 = float64(lat1) / 1000
	out.lon1 = float64(lon1) / 1000
	lat2f := float64(lat2) / 1000
	lon2f := float64(lon2) / 1000
	if out.ni > 1 {
		if di > 0 {
			out.dLon = float64(di) / 1000
		} else {
			out.dLon = (lon2f - out.lon1) / float64(out.ni-1)
		}
	}
	if out.nj > 1 {
		if dj > 0 {
			out.dLat = float64(dj) / 1000
		} else {
			out.dLat = (lat2f - out.lat1) / float64(out.nj-1)
		}
	}
	// scanning mode bit 0: 0 = +i west to east; bit 1: 0 = +j south to north
	scan := gds[27]
	if scan&0x40 != 0 { // j decreases (north to south)
		out.dLat = -math.Abs(out.dLat)
		if out.dLat == 0 {
			out.dLat = (lat2f - out.lat1) / float64(max(out.nj-1, 1))
		}
	} else if out.dLat < 0 {
		out.dLat = -out.dLat
	}

	bdsStart := gdsStart + gdsLen
	// Optional BMS
	if pds[7]&0x40 != 0 || (len(pds) > 7 && int(pds[6]) != 255 && false) {
		// Section presence flags in PDS octet 8 (index 7): bit1 GDS, bit2 BMS
	}
	if pds[7]&0x20 != 0 { // BMS present
		if bdsStart+3 > len(msg) {
			return out, fmt.Errorf("bad bms")
		}
		bmsLen := int(msg[bdsStart])<<16 | int(msg[bdsStart+1])<<8 | int(msg[bdsStart+2])
		bdsStart += bmsLen
	}
	if bdsStart+11 > len(msg) {
		return out, fmt.Errorf("bad bds")
	}
	bdsLen := int(msg[bdsStart])<<16 | int(msg[bdsStart+1])<<8 | int(msg[bdsStart+2])
	if bdsLen < 11 || bdsStart+bdsLen > len(msg) {
		return out, fmt.Errorf("bad bds len")
	}
	bds := msg[bdsStart : bdsStart+bdsLen]
	flags := bds[3]
	if flags&0x80 != 0 {
		return out, fmt.Errorf("spherical harmonics unsupported")
	}
	scale := int16(binary.BigEndian.Uint16(bds[4:6]))
	refBits := binary.BigEndian.Uint32(bds[6:10])
	ref := math.Float32frombits(refBits)
	nbits := int(bds[10])
	if nbits == 0 {
		out.values = make([]float64, out.ni*out.nj)
		for i := range out.values {
			out.values[i] = float64(ref)
		}
		return out, nil
	}
	dataBytes := bds[11:]
	count := out.ni * out.nj
	out.values = make([]float64, count)
	var bitPos int
	factor := math.Pow(2, float64(-int(scale)))
	for i := 0; i < count; i++ {
		var x uint32
		for b := 0; b < nbits; b++ {
			byteIdx := bitPos / 8
			if byteIdx >= len(dataBytes) {
				return out, fmt.Errorf("bds truncated")
			}
			bit := 7 - (bitPos % 8)
			x = (x << 1) | uint32((dataBytes[byteIdx]>>bit)&1)
			bitPos++
		}
		out.values[i] = float64(ref) + float64(x)*factor
	}
	return out, nil
}

func forecastOffset(unit, p1 int) time.Duration {
	switch unit {
	case 0: // minute
		return time.Duration(p1) * time.Minute
	case 1: // hour
		return time.Duration(p1) * time.Hour
	case 2: // day
		return time.Duration(p1) * 24 * time.Hour
	case 10: // 3 hours
		return time.Duration(p1) * 3 * time.Hour
	case 11: // 6 hours
		return time.Duration(p1) * 6 * time.Hour
	case 12: // 12 hours
		return time.Duration(p1) * 12 * time.Hour
	default:
		return time.Duration(p1) * time.Hour
	}
}

func interpolate(m gridMsg, lat, lon float64) (float64, bool) {
	if m.ni < 2 || m.nj < 2 || len(m.values) < m.ni*m.nj || m.dLat == 0 || m.dLon == 0 {
		return 0, false
	}
	// Normalize lon to grid range roughly
	lon1 := m.lon1
	if lon < lon1-180 {
		lon += 360
	}
	if lon > lon1+180 {
		lon -= 360
	}
	fi := (lon - m.lon1) / m.dLon
	fj := (lat - m.lat1) / m.dLat
	if fi < -0.5 || fj < -0.5 || fi > float64(m.ni-1)+0.5 || fj > float64(m.nj-1)+0.5 {
		return 0, false
	}
	i0 := int(math.Floor(fi))
	j0 := int(math.Floor(fj))
	if i0 < 0 {
		i0 = 0
	}
	if j0 < 0 {
		j0 = 0
	}
	if i0 > m.ni-2 {
		i0 = m.ni - 2
	}
	if j0 > m.nj-2 {
		j0 = m.nj - 2
	}
	dx := fi - float64(i0)
	dy := fj - float64(j0)
	v00 := m.values[j0*m.ni+i0]
	v10 := m.values[j0*m.ni+i0+1]
	v01 := m.values[(j0+1)*m.ni+i0]
	v11 := m.values[(j0+1)*m.ni+i0+1]
	v0 := v00*(1-dx) + v10*dx
	v1 := v01*(1-dx) + v11*dx
	return v0*(1-dy) + v1*dy, true
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
