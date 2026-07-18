package main

import (
	"testing"
	"time"
)

func TestMaySpawnFirstCallAllowed(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if !maySpawn(dir, now) {
		t.Fatal("first call on an empty dir should allow spawning")
	}
}

func TestMaySpawnCooldownBlocksImmediateRetry(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if !maySpawn(dir, now) {
		t.Fatal("first call should allow spawning")
	}
	if maySpawn(dir, now.Add(1*time.Second)) {
		t.Fatal("call within the cooldown window should not allow spawning again")
	}
}

func TestMaySpawnAllowedAgainAfterCooldown(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if !maySpawn(dir, now) {
		t.Fatal("first call should allow spawning")
	}
	later := now.Add(spawnCooldown + time.Second)
	if !maySpawn(dir, later) {
		t.Fatal("call after the cooldown window has elapsed should allow spawning again")
	}
}
