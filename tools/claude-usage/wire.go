package main

import (
	"fmt"
	"math"
	"strings"
	"time"
)

// pct rounds a 0-100 float to an int and clamps it.
func pct(f float64) int {
	v := int(math.Round(f))
	if v < 0 {
		return 0
	}
	if v > 100 {
		return 100
	}
	return v
}

// secsUntil returns whole seconds from now to epoch, never negative.
func secsUntil(epoch int64, now time.Time) int64 {
	d := epoch - now.Unix()
	if d < 0 {
		return 0
	}
	return d
}

// UsageLine renders the @U line. Absent windows are omitted entirely: the
// firmware's parseUsageLine keeps prior values for fields it does not see, so
// omission is how we say "unchanged". Returns ok=false when there is nothing
// worth sending, in which case the board keeps its last value.
func UsageLine(p *Payload, tier string, now time.Time) (string, bool) {
	if p == nil || p.RateLimits == nil {
		return "", false
	}
	five, seven := p.RateLimits.FiveHour, p.RateLimits.SevenDay
	if five == nil && seven == nil {
		return "", false
	}

	parts := []string{"@U"}
	if five != nil {
		parts = append(parts, fmt.Sprintf("five=%d", pct(five.UsedPercentage)))
	}
	if seven != nil {
		parts = append(parts, fmt.Sprintf("seven=%d", pct(seven.UsedPercentage)))
	}
	if tier != "" {
		parts = append(parts, "sub="+tier)
	}
	// Omit *_s when resets_at is absent: a nil there is "unknown", not "now".
	// The board keeps its prior countdown (omission = unchanged) rather than
	// being told the limit resets imminently.
	if five != nil && five.ResetsAt != nil {
		parts = append(parts, fmt.Sprintf("five_s=%d", secsUntil(*five.ResetsAt, now)))
	}
	if seven != nil && seven.ResetsAt != nil {
		parts = append(parts, fmt.Sprintf("seven_s=%d", secsUntil(*seven.ResetsAt, now)))
	}
	return strings.Join(parts, "|") + "\n", true
}
