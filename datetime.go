package rawhttp

import (
	"sync/atomic"
	"time"
)

// HTTP-date header line, e.g. "Date: Mon, 02 Jan 2006 15:04:05 GMT\r\n" (37 bytes).
const dateHeaderLen = 37

var (
	dateSec  atomic.Int64
	dateLine atomic.Pointer[[]byte]
)

func init() {
	refreshDate(time.Now().UTC())
	go dateUpdater()
}

func dateUpdater() {
	for {
		now := time.Now().UTC()
		// Sleep until the next whole second (+tiny skew) to avoid busy loop.
		next := now.Truncate(time.Second).Add(time.Second)
		time.Sleep(time.Until(next))
		refreshDate(time.Now().UTC())
	}
}

func refreshDate(t time.Time) {
	t = t.UTC().Truncate(time.Second)
	sec := t.Unix()
	b := make([]byte, 0, dateHeaderLen)
	b = append(b, "Date: "...)
	b = appendHTTPDate(b, t)
	b = append(b, '\r', '\n')
	dateSec.Store(sec)
	dateLine.Store(&b)
}

// appendHTTPDate appends t in RFC 1123 / HTTP-date GMT form (no "UTC" suffix).
func appendHTTPDate(dst []byte, t time.Time) []byte {
	// Mon, 02 Jan 2006 15:04:05 GMT
	dst = append(dst, dayNames[t.Weekday()]...)
	dst = append(dst, ',', ' ')
	d := t.Day()
	dst = append(dst, byte('0'+d/10), byte('0'+d%10), ' ')
	dst = append(dst, monthNames[t.Month()]...)
	dst = append(dst, ' ')
	y := t.Year()
	dst = append(dst,
		byte('0'+y/1000),
		byte('0'+(y/100)%10),
		byte('0'+(y/10)%10),
		byte('0'+y%10),
		' ')
	h, m, s := t.Clock()
	dst = append(dst,
		byte('0'+h/10), byte('0'+h%10), ':',
		byte('0'+m/10), byte('0'+m%10), ':',
		byte('0'+s/10), byte('0'+s%10),
		' ', 'G', 'M', 'T')
	return dst
}

var dayNames = [...]string{
	"Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat",
}

var monthNames = [...]string{
	"", "Jan", "Feb", "Mar", "Apr", "May", "Jun",
	"Jul", "Aug", "Sep", "Oct", "Nov", "Dec",
}

func currentDateHeader() ([]byte, int64) {
	sec := dateSec.Load()
	p := dateLine.Load()
	if p == nil {
		refreshDate(time.Now().UTC())
		sec = dateSec.Load()
		p = dateLine.Load()
	}
	return *p, sec
}

func currentDateSec() int64 {
	sec := dateSec.Load()
	if sec == 0 {
		refreshDate(time.Now().UTC())
		sec = dateSec.Load()
	}
	return sec
}
