package main

// Payload is the subset of Claude Code's statusline stdin JSON we use.
// Schema: https://code.claude.com/docs/en/statusline
type Payload struct {
	SessionID  string      `json:"session_id"`
	RateLimits *RateLimits `json:"rate_limits"`
	Cost       struct {
		TotalCostUSD float64 `json:"total_cost_usd"`
	} `json:"cost"`
}

// RateLimits is absent entirely until a session's first API response, and for
// non-subscribers. Each window is independently absent, hence the pointers.
type RateLimits struct {
	FiveHour *Window `json:"five_hour"`
	SevenDay *Window `json:"seven_day"`
}

// Window is one rate-limit window. ResetsAt is Unix epoch seconds; a pointer so
// an absent resets_at (nil) is distinguishable from "resets now" (0). When it is
// nil we omit five_s/seven_s entirely — the "omission = unchanged" contract —
// rather than a non-pointer zero that would tell the board a reset is imminent.
type Window struct {
	UsedPercentage float64 `json:"used_percentage"`
	ResetsAt       *int64  `json:"resets_at"`
}
