package openwrf

import (
	"bufio"
	"bytes"
	"fmt"
	"hash/crc32"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/ben/ikite-go/internal/models"
	"github.com/ledongthuc/pdf"
)

const (
	ModelID   = 1000001
	ModelName = "openWRF"

	folderViewURL = "https://drive.google.com/embeddedfolderview?id=1IPRb1cSMBmTSzPdUK2rQAAhF7KuWwywr#list"
	maxFolderSize = 2 << 20
	maxPDFSize    = 10 << 20
)

var (
	periodPattern = regexp.MustCompile(`^(\d{2})(\d{2})-(\d{2})(\d{2})$`)
	fileIDPattern = regexp.MustCompile(`https://drive\.google\.com/file/d/([A-Za-z0-9_-]+)/view`)
)

// Explicit aliases from settings spot id → preferred Drive PDF stem
// (without the 18z_1km_ prefix / .pdf suffix). Prefer kite-spot PDFs over
// *_wind_station variants.
var spotPDFAliases = map[string]string{
	"ky":    "Kiryat_Yam",
	"bg":    "Bat_Galim_Club",
	"st":    "Shavei_Tzion",
	"15233": "Betzet",
	"2256":  "Atlit_Mivtzar",
	"1909":  "Kineret_Diamond",
	"2752":  "Kineret_Migdal", // Sea of G
	"3379":  "Kineret_Gino",
}

type Client struct {
	HTTPClient *http.Client
	PDFURL     string // optional override: single fixed PDF URL (testing / one-spot)
}

type DriveFile struct {
	ID   string
	Name string
}

type SpotMapping struct {
	Spot     models.Spot
	PDF      DriveFile
	Forecast int // storage key used as wind_forecast.windguru_id
}

func New(pdfURL string) *Client {
	return &Client{
		HTTPClient: &http.Client{Timeout: 45 * time.Second},
		PDFURL:     pdfURL,
	}
}

// ForecastKey returns a stable wind_forecast.windguru_id for a spot.
func ForecastKey(sp models.Spot) int {
	if sp.WindguruID != nil && *sp.WindguruID > 0 {
		return *sp.WindguruID
	}
	if sp.WindguruStationID != nil && *sp.WindguruStationID > 0 {
		return *sp.WindguruStationID
	}
	h := crc32.ChecksumIEEE([]byte("openwrf:" + sp.ID))
	return int(8_000_000 + (h % 1_000_000))
}

func (c *Client) ListPDFs() ([]DriveFile, error) {
	resp, err := c.HTTPClient.Get(folderViewURL)
	if err != nil {
		return nil, fmt.Errorf("list Google Drive folder: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("list Google Drive folder: HTTP %s", resp.Status)
	}

	data, err := io.ReadAll(io.LimitReader(resp.Body, maxFolderSize+1))
	if err != nil {
		return nil, fmt.Errorf("read Google Drive folder: %w", err)
	}
	if len(data) > maxFolderSize {
		return nil, fmt.Errorf("read Google Drive folder: exceeds %d bytes", maxFolderSize)
	}

	var files []DriveFile
	for _, entry := range strings.Split(string(data), `<div class="flip-entry"`) {
		match := fileIDPattern.FindStringSubmatch(entry)
		if match == nil {
			continue
		}
		titleStart := strings.Index(entry, `flip-entry-title">`)
		if titleStart < 0 {
			continue
		}
		titleStart += len(`flip-entry-title">`)
		titleEnd := strings.Index(entry[titleStart:], "<")
		if titleEnd < 0 {
			continue
		}
		name := entry[titleStart : titleStart+titleEnd]
		if !strings.HasSuffix(strings.ToLower(name), ".pdf") {
			continue
		}
		files = append(files, DriveFile{ID: match[1], Name: name})
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("no PDF files found in Google Drive folder")
	}
	return files, nil
}

// MatchSpots maps settings spots to kite-spot PDFs (skips *_wind_station).
func MatchSpots(spots []models.Spot, files []DriveFile) []SpotMapping {
	byStem := map[string]DriveFile{}
	for _, f := range files {
		stem := pdfStem(f.Name)
		if strings.HasSuffix(stem, "_wind_station") {
			continue
		}
		byStem[strings.ToLower(stem)] = f
	}

	var out []SpotMapping
	used := map[string]bool{}
	for _, sp := range spots {
		pdf, ok := matchSpotPDF(sp, byStem)
		if !ok || used[pdf.ID] {
			continue
		}
		used[pdf.ID] = true
		out = append(out, SpotMapping{
			Spot:     sp,
			PDF:      pdf,
			Forecast: ForecastKey(sp),
		})
	}
	return out
}

func matchSpotPDF(sp models.Spot, byStem map[string]DriveFile) (DriveFile, bool) {
	if alias, ok := spotPDFAliases[sp.ID]; ok {
		if f, ok := byStem[strings.ToLower(alias)]; ok {
			return f, true
		}
	}

	spotNorm := normalizeName(sp.Name)
	if spotNorm == "" {
		return DriveFile{}, false
	}

	// Exact stem match after normalizing underscores/spaces.
	for stem, f := range byStem {
		if normalizeName(stem) == spotNorm {
			return f, true
		}
	}

	// Prefix / contains: prefer shortest stem that fully covers the spot name.
	var best DriveFile
	bestLen := 1 << 30
	for stem, f := range byStem {
		n := normalizeName(stem)
		if strings.HasPrefix(n, spotNorm) || strings.HasPrefix(spotNorm, n) || strings.Contains(n, spotNorm) {
			if len(n) < bestLen {
				best = f
				bestLen = len(n)
			}
		}
	}
	if best.ID != "" {
		return best, true
	}
	return DriveFile{}, false
}

func pdfStem(name string) string {
	base := strings.TrimSuffix(name, ".pdf")
	base = strings.TrimSuffix(base, ".PDF")
	base = strings.TrimPrefix(base, "18z_1km_")
	return base
}

func normalizeName(s string) string {
	s = strings.ToLower(s)
	var b strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func (c *Client) downloadURL(fileID string) string {
	return "https://drive.usercontent.google.com/download?id=" + fileID + "&export=download&confirm=t"
}

func (c *Client) FetchFile(file DriveFile, reference time.Time, loc *time.Location) ([]models.WindForecastRow, error) {
	return c.fetchURL(c.downloadURL(file.ID), reference, loc)
}

func (c *Client) Fetch(reference time.Time, loc *time.Location) ([]models.WindForecastRow, error) {
	if c.PDFURL != "" {
		return c.fetchURL(c.PDFURL, reference, loc)
	}
	files, err := c.ListPDFs()
	if err != nil {
		return nil, err
	}
	for _, f := range files {
		if f.Name == "18z_1km_Kiryat_Yam.pdf" {
			return c.FetchFile(f, reference, loc)
		}
	}
	return nil, fmt.Errorf("18z_1km_Kiryat_Yam.pdf not found in Google Drive folder")
}

func (c *Client) fetchURL(pdfURL string, reference time.Time, loc *time.Location) ([]models.WindForecastRow, error) {
	resp, err := c.HTTPClient.Get(pdfURL)
	if err != nil {
		return nil, fmt.Errorf("download PDF: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download PDF: HTTP %s", resp.Status)
	}

	data, err := io.ReadAll(io.LimitReader(resp.Body, maxPDFSize+1))
	if err != nil {
		return nil, fmt.Errorf("read PDF: %w", err)
	}
	if len(data) > maxPDFSize {
		return nil, fmt.Errorf("read PDF: exceeds %d bytes", maxPDFSize)
	}
	if !bytes.HasPrefix(data, []byte("%PDF-")) {
		return nil, fmt.Errorf("download PDF: response is not a PDF")
	}

	text, err := extractText(data)
	if err != nil {
		return nil, err
	}
	return Parse(text, reference, loc)
}

func extractText(data []byte) (string, error) {
	r, err := pdf.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return "", fmt.Errorf("open PDF: %w", err)
	}
	textReader, err := r.GetPlainText()
	if err != nil {
		return "", fmt.Errorf("extract PDF text: %w", err)
	}
	text, err := io.ReadAll(textReader)
	if err != nil {
		return "", fmt.Errorf("read PDF text: %w", err)
	}
	return string(text), nil
}

func Parse(text string, reference time.Time, loc *time.Location) ([]models.WindForecastRow, error) {
	if loc == nil {
		return nil, fmt.Errorf("timezone is required")
	}
	reference = reference.In(loc)

	var rows []models.WindForecastRow
	var previous time.Time
	seen := make(map[time.Time]bool)
	scanner := bufio.NewScanner(strings.NewReader(text))
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) != 4 {
			continue
		}

		wind, errWind := strconv.ParseFloat(fields[1], 64)
		dir, errDir := strconv.ParseFloat(fields[2], 64)
		gust, errGust := strconv.ParseFloat(fields[3], 64)
		if errWind != nil || errDir != nil || errGust != nil || wind < 0 || gust < 0 || dir < 0 || dir > 360 {
			continue
		}

		period, err := parsePeriod(fields[0], reference, loc)
		if err != nil {
			// The source PDF currently has one malformed text glyph for 23:30
			// even though the rendered page is correct. Preserve the 30-minute
			// sequence when a date-like row follows a valid row.
			if previous.IsZero() || len(fields[0]) < 5 || fields[0][4] != '-' {
				continue
			}
			period = previous.Add(30 * time.Minute)
		}
		if seen[period] {
			return nil, fmt.Errorf("duplicate forecast period %s", period)
		}
		seen[period] = true
		previous = period

		w, g, d := wind, gust, dir
		day := dateOnly(period)
		rows = append(rows, models.WindForecastRow{
			ForecastDate: day,
			IDModel:      ModelID,
			Model:        ModelName,
			Period:       period,
			Wind:         &w,
			Gust:         &g,
			WindDir:      &d,
		})
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan forecast text: %w", err)
	}
	if len(rows) == 0 {
		return nil, fmt.Errorf("no forecast rows found in PDF")
	}
	return rows, nil
}

func dateOnly(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
}

func parsePeriod(value string, reference time.Time, loc *time.Location) (time.Time, error) {
	match := periodPattern.FindStringSubmatch(value)
	if match == nil {
		return time.Time{}, fmt.Errorf("invalid period %q", value)
	}
	month, _ := strconv.Atoi(match[1])
	day, _ := strconv.Atoi(match[2])
	hour, _ := strconv.Atoi(match[3])
	minute, _ := strconv.Atoi(match[4])
	if hour > 23 || minute > 59 {
		return time.Time{}, fmt.Errorf("invalid period %q", value)
	}

	period := time.Date(reference.Year(), time.Month(month), day, hour, minute, 0, 0, loc)
	if period.Month() != time.Month(month) || period.Day() != day {
		return time.Time{}, fmt.Errorf("invalid period %q", value)
	}
	if period.Before(reference.AddDate(0, -6, 0)) {
		period = period.AddDate(1, 0, 0)
	} else if period.After(reference.AddDate(0, 6, 0)) {
		period = period.AddDate(-1, 0, 0)
	}
	return period, nil
}
