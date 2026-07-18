package main

import (
	"testing"
	"time"
)

func at(sec int64) time.Time { return time.Unix(sec, 0) }

func p(five, seven *Window) *Payload {
	return &Payload{RateLimits: &RateLimits{FiveHour: five, SevenDay: seven}}
}

// win builds a Window carrying a resets_at, for the seconds-remaining tests.
func win(pct float64, resetsAt int64) *Window {
	r := resetsAt
	return &Window{UsedPercentage: pct, ResetsAt: &r}
}

func TestUsageLine(t *testing.T) {
	tests := []struct {
		name string
		in   *Payload
		tier string
		now  time.Time
		want string
		ok   bool
	}{
		{
			name: "both windows",
			in:   p(win(23.5, 1000+3120), win(41.2, 1000+280800)),
			tier: "Max 20x", now: at(1000),
			want: "@U|five=24|seven=41|sub=Max 20x|five_s=3120|seven_s=280800\n",
			ok:   true,
		},
		{
			name: "five only - absent seven is omitted, board keeps prior",
			in:   p(win(10.0, 1000+60), nil),
			tier: "Max 20x", now: at(1000),
			want: "@U|five=10|sub=Max 20x|five_s=60\n",
			ok:   true,
		},
		{
			name: "past reset clamps to zero, never negative",
			in:   p(win(99.9, 500), nil),
			tier: "Pro", now: at(1000),
			want: "@U|five=100|sub=Pro|five_s=0\n",
			ok:   true,
		},
		{
			name: "window present but resets_at absent - five_s omitted, not five_s=0",
			in:   p(&Window{UsedPercentage: 12.0}, nil),
			tier: "Max 20x", now: at(1000),
			want: "@U|five=12|sub=Max 20x\n",
			ok:   true,
		},
		{
			name: "no rate_limits at all - nothing to send",
			in:   &Payload{},
			tier: "Max 20x", now: at(1000),
			want: "", ok: false,
		},
		{
			name: "rate_limits present but both windows nil",
			in:   p(nil, nil),
			tier: "Max 20x", now: at(1000),
			want: "", ok: false,
		},
		{
			name: "empty tier omits the sub field",
			in:   p(win(5.0, 1000+120), nil),
			tier: "", now: at(1000),
			want: "@U|five=5|five_s=120\n",
			ok:   true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := UsageLine(tc.in, tc.tier, tc.now)
			if ok != tc.ok || got != tc.want {
				t.Errorf("UsageLine() = %q,%v; want %q,%v", got, ok, tc.want, tc.ok)
			}
		})
	}
}
