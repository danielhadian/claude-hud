//go:build unix

package main

import "testing"

func TestFindPortHonoursExplicitConfig(t *testing.T) {
	got, err := FindPort(Config{Port: "/dev/ttyFAKE"})
	if err != nil || got != "/dev/ttyFAKE" {
		t.Errorf("explicit port = %q,%v; want /dev/ttyFAKE,nil", got, err)
	}
}

func TestFindPortAutoErrorsWhenNothingPresent(t *testing.T) {
	// candidateGlobs is overridable so auto-detect is testable without hardware.
	old := candidateGlobs
	candidateGlobs = []string{"/nonexistent/ttyUSB*"}
	defer func() { candidateGlobs = old }()

	if _, err := FindPort(Config{Port: "auto"}); err == nil {
		t.Fatal("auto with no devices must error, not return empty string")
	}
}
