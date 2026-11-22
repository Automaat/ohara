package deals

import "testing"

func TestSelfTitledAlbumRequiresAlbumEvidence(t *testing.T) {
	tests := []struct {
		name  string
		album string
		alias *string
		title string
		match bool
	}{
		{name: "Parlophone alias", album: "David Bowie aka Space Oddity", title: "David Bowie – Space Oddity (Parlophone 2016) LP", match: true},
		{name: "alias field", album: "David Bowie", alias: new("Space Oddity"), title: "Space Oddity – David Bowie winyl", match: true},
		{name: "artist and medium", album: "David Bowie", alias: new("Space Oddity"), title: "David Bowie LP"},
		{name: "Hunky Dory", album: "David Bowie aka Space Oddity", title: "David Bowie – Hunky Dory Parlophone 2016 LP"},
		{name: "Low", album: "David Bowie", alias: new("Space Oddity"), title: "David Bowie Low vinyl"},
		{name: "Blackstar", album: "David Bowie", title: "David Bowie Blackstar LP"},
		{name: "1967 debut", album: "David Bowie aka Space Oddity", title: "David Bowie – David Bowie (1967) LP"},
		{name: "alias words separated", album: "David Bowie aka Space Oddity", title: "David Bowie Space Rock with bonus Oddity LP"},
		{name: "wrong medium", album: "David Bowie aka Space Oddity", title: "David Bowie Space Oddity CD"},
		{name: "ordinary title", album: "Low", title: "David Bowie – Low LP", match: true},
		{name: "ordinary title without artist", album: "Low", title: "Low LP"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := Want{Kind: "music", Name: tt.album, OriginalTitle: tt.alias, Authors: []string{"David Bowie"}, Format: new("vinyl")}
			if got := w.Matches(tt.title); got != tt.match {
				t.Errorf("Matches(%q) = %t, want %t", tt.title, got, tt.match)
			}
		})
	}
}

func TestSelfTitledAlbumRequiresTheAliasAsATitle(t *testing.T) {
	tests := []struct {
		name   string
		album  string
		artist string
		title  string
		match  bool
	}{
		{"explicit multiword alias", "The Beatles aka White Album", "The Beatles", "The Beatles – White Album LP", true},
		{"medium between alias words", "The Beatles aka White Album", "The Beatles", "The Beatles – Abbey Road white vinyl album LP", false},
		{"alias inside another title", "The Beatles aka White Album", "The Beatles", "The Beatles – Abbey Road White Album LP", false},
		{"explicit single-word alias", "Peter Gabriel aka Scratch", "Peter Gabriel", "Peter Gabriel – Scratch LP", true},
		{"longer album title", "Peter Gabriel aka Scratch", "Peter Gabriel", "Peter Gabriel – Scratch My Back 2LP winyl", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := Want{Kind: "music", Name: tt.album, Authors: []string{tt.artist}, Format: new("vinyl")}
			if got := w.Matches(tt.title); got != tt.match {
				t.Errorf("Matches(%q) = %t, want %t", tt.title, got, tt.match)
			}
		})
	}
}

func TestSelfTitledAlbumMatchesByDiscogsRelease(t *testing.T) {
	wants := []Want{{
		Kind: "music", Name: "David Bowie", Authors: []string{"David Bowie"},
		Format: new("vinyl"), DiscogsReleaseID: new(int64(101)),
	}}
	tests := []struct {
		name      string
		source    string
		releaseID int64
		medium    string
		match     bool
	}{
		{"same release", DiscogsSource, 101, "vinyl", true},
		{"different release", DiscogsSource, 102, "vinyl", false},
		{"missing release", DiscogsSource, 0, "vinyl", false},
		{"different medium", DiscogsSource, 101, "cd", false},
		{"other source", "olx", 101, "vinyl", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			listing := Listing{Title: "David Bowie - David Bowie", Source: tt.source, Medium: tt.medium, DiscogsReleaseID: tt.releaseID}
			if got := pick(wants, &listing, Rules{}, Titles{}) != nil; got != tt.match {
				t.Errorf("listing matched = %t, want %t", got, tt.match)
			}
		})
	}
}
