package main

import "testing"

func TestStatusText(t *testing.T) {
	tests := []struct {
		name string
		in   *Payload
		want string
	}{
		{
			name: "both windows render both percentages",
			in:   p(&Window{UsedPercentage: 23.5}, &Window{UsedPercentage: 41.2}),
			want: "5h 24% · 7d 41%",
		},
		{
			name: "five only",
			in:   p(&Window{UsedPercentage: 7.0}, nil),
			want: "5h 7%",
		},
		{
			name: "no rate limits yet - say so rather than lie",
			in:   &Payload{},
			want: "usage —",
		},
		{
			name: "nil payload never panics",
			in:   nil,
			want: "usage —",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := StatusText(tc.in); got != tc.want {
				t.Errorf("StatusText() = %q; want %q", got, tc.want)
			}
		})
	}
}
