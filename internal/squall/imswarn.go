package squall

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

// IMS official warnings (https://ims.gov.il/en/warnings), from the JSON the IMS
// site itself loads. A warning is relevant when it covers one of the regions
// around Haifa Bay and is of a kind that matters on the water.

const imsWarningsURL = "https://ims.gov.il/he/warnings"

// imsBayRegions are the IMS warning regions covering Kiryat Haim.
var imsBayRegions = map[string]string{
	"r-89": "Zevulun Valley",
	"r-97": "Zevulun Valley",
	"r-94": "Carmel Coast",
	"r-54": "Sea (North)",
}

// imsWarningKinds are the warning types sent, with an English name.
var imsWarningKinds = map[int]string{
	2:  "strong winds",
	3:  "dangerous sea",
	4:  "high sea",
	11: "thunderstorms",
	14: "wind shear",
	51: "news flash",
	52: "news flash",
}

// imsThunderKinds mark a convective day (shown on radar and lightning alerts).
var imsThunderKinds = map[int]bool{11: true, 51: true, 52: true}

var imsSeverity = map[int]string{
	0: "", 2: "early yellow", 3: "yellow", 4: "orange", 5: "early orange", 6: "early red", 7: "red", 8: "",
}

// IMSWarningItem is one parsed warning.
type IMSWarningItem struct {
	WID                int64
	TypeID, SeverityID int
	ValidFrom, ValidTo time.Time
	Regions            []string
	TextEN, TextHE     string
}

type imsWarningRaw struct {
	WID           string `json:"wid"`
	SeverityID    string `json:"severity_id"`
	WarningTypeID string `json:"warning_type_id"`
	ValidFrom     string `json:"valid_from"`
	ValidTo       string `json:"valid_to"`
	FullEN        string `json:"full_en"`
	Text          string `json:"text"`
}

// ParseIMSWarnings picks the relevant warnings out of the /warnings JSON.
func ParseIMSWarnings(body []byte, tz *time.Location) ([]IMSWarningItem, error) {
	var r struct {
		Data struct {
			Full json.RawMessage `json:"full_warnings_data"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &r); err != nil {
		return nil, fmt.Errorf("decode IMS warnings: %w", err)
	}
	// With no warnings out IMS sends an empty array instead of an object.
	var full map[string]map[string]map[string]imsWarningRaw
	if raw := strings.TrimSpace(string(r.Data.Full)); raw != "" && raw != "[]" && raw != "null" {
		if err := json.Unmarshal(r.Data.Full, &full); err != nil {
			return nil, fmt.Errorf("decode IMS warnings: %w", err)
		}
	}
	byWID := map[int64]*IMSWarningItem{}
	for _, regions := range full {
		for rid, ws := range regions {
			if _, ok := imsBayRegions[rid]; !ok {
				continue
			}
			for _, w := range ws {
				typ, _ := strconv.Atoi(w.WarningTypeID)
				if _, ok := imsWarningKinds[typ]; !ok {
					continue
				}
				wid, err := strconv.ParseInt(w.WID, 10, 64)
				if err != nil {
					continue
				}
				it := byWID[wid]
				if it == nil {
					sev, _ := strconv.Atoi(w.SeverityID)
					from, err1 := time.ParseInLocation("2006-01-02 15:04:05", w.ValidFrom, tz)
					to, err2 := time.ParseInLocation("2006-01-02 15:04:05", w.ValidTo, tz)
					if err1 != nil || err2 != nil {
						continue
					}
					it = &IMSWarningItem{WID: wid, TypeID: typ, SeverityID: sev, ValidFrom: from, ValidTo: to,
						TextEN: strings.Join(strings.Fields(w.FullEN), " "), TextHE: strings.TrimSpace(w.Text)}
					byWID[wid] = it
				}
				it.Regions = append(it.Regions, rid)
			}
		}
	}
	out := make([]IMSWarningItem, 0, len(byWID))
	for _, it := range byWID {
		sort.Strings(it.Regions)
		out = append(out, *it)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ValidFrom.Before(out[j].ValidFrom) })
	return out, nil
}

// hasHebrew reports whether s contains Hebrew letters. Messages go out in
// English only; a translation that comes back still in Hebrew is dropped.
func hasHebrew(s string) bool {
	for _, r := range s {
		if r >= 0x0590 && r <= 0x05FF {
			return true
		}
	}
	return false
}

// imsWarningMessage is English only. IMS publishes the headline in English and
// the details in Hebrew; detailsEN is the details machine-translated, or "".
func imsWarningMessage(w IMSWarningItem, detailsEN string) string {
	var b strings.Builder
	sev := imsSeverity[w.SeverityID]
	kind := imsWarningKinds[w.TypeID]
	if sev != "" {
		fmt.Fprintf(&b, "⚠️ IMS %s warning: %s\n", sev, kind)
	} else {
		fmt.Fprintf(&b, "⚠️ IMS update: %s\n", kind)
	}
	fmt.Fprintf(&b, "Valid %s – %s.\n", w.ValidFrom.Format("Mon 15:04"), w.ValidTo.Format("Mon 15:04"))
	if w.TextEN != "" && !hasHebrew(w.TextEN) {
		b.WriteString(w.TextEN + "\n")
	}
	if detailsEN = strings.TrimSpace(detailsEN); detailsEN != "" && !hasHebrew(detailsEN) {
		b.WriteString("Details (auto-translated): " + detailsEN + "\n")
	}
	if imsThunderKinds[w.TypeID] {
		b.WriteString("Squall alerts stay on: radar, lightning and Bat Galim will warn if a cell heads for the bay.\n")
	}
	b.WriteString("https://ims.gov.il/en/warnings")
	return b.String()
}
