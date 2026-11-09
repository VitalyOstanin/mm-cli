package render

import "time"

// Time layouts of text output: TimeLayout without the zone, TimeZoneLayout
// with its abbreviation.
const (
	TimeLayout     = "2006-01-02 15:04"
	TimeZoneLayout = TimeLayout + " MST"
)

// RFC3339 formats Unix milliseconds in UTC for JSON output; 0 or less, which
// the server sends for "not set", gives "".
func RFC3339(ms int64) string {
	if ms <= 0 {
		return ""
	}
	return time.UnixMilli(ms).UTC().Format(time.RFC3339)
}

// LocalTime shows an RFC 3339 time of JSON output in loc for text output; a
// value that does not parse comes back unchanged.
func LocalTime(s string, loc *time.Location) string {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return s
	}
	return t.In(loc).Format(TimeLayout)
}
