package main

import (
	"fmt"
	"strings"
)

// StatusText is what Claude Code shows in the status line. Must never panic:
// it runs on every assistant message.
func StatusText(p *Payload) string {
	if p == nil || p.RateLimits == nil {
		return "usage —"
	}
	var parts []string
	if f := p.RateLimits.FiveHour; f != nil {
		parts = append(parts, fmt.Sprintf("5h %d%%", pct(f.UsedPercentage)))
	}
	if s := p.RateLimits.SevenDay; s != nil {
		parts = append(parts, fmt.Sprintf("7d %d%%", pct(s.UsedPercentage)))
	}
	if len(parts) == 0 {
		return "usage —"
	}
	return strings.Join(parts, " · ")
}
