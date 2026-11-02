package main

import (
	"math"
	"testing"
)

func TestLocationCatalogHasUnique47Prefectures(t *testing.T) {
	if got := len(locationCatalog); got != 47 {
		t.Fatalf("location catalog has %d entries, want 47", got)
	}

	seen := make(map[string]struct{}, len(locationCatalog))
	for _, location := range locationCatalog {
		if _, exists := seen[location.Prefecture]; exists {
			t.Fatalf("duplicate prefecture in location catalog: %s", location.Prefecture)
		}
		seen[location.Prefecture] = struct{}{}
	}
}

func TestResolveLocationAcceptsClientRoundedCoordinates(t *testing.T) {
	for _, want := range locationCatalog {
		lat := math.Round(want.Latitude*10) / 10
		lon := math.Round(want.Longitude*10) / 10

		got, ok := resolveLocation(lat, lon)
		if !ok {
			t.Fatalf("rounded coordinates for %s were rejected", want.Prefecture)
		}
		if got.Prefecture != want.Prefecture {
			t.Fatalf("rounded coordinates resolved to %s, want %s", got.Prefecture, want.Prefecture)
		}
	}
}

func TestResolveLocationRejectsArbitraryCoordinates(t *testing.T) {
	if _, ok := resolveLocation(0, 0); ok {
		t.Fatal("arbitrary coordinates should not select an upstream location")
	}
}

func TestResolveLocationAcceptsDefaultTokyoCoordinates(t *testing.T) {
	got, ok := resolveLocation(defaultLat, defaultLon)
	if !ok {
		t.Fatal("default Tokyo coordinates were rejected")
	}
	if got.Prefecture != "東京都" {
		t.Fatalf("default coordinates resolved to %s, want 東京都", got.Prefecture)
	}
}
