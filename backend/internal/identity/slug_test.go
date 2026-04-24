package identity

import (
	"strings"
	"testing"
)

func TestSlugify(t *testing.T) {
	cases := map[string]string{
		"Personal":                "personal",
		"David's Household":       "davids-household",
		"Étoiles & Co":            "etoiles-co",
		"   leading trailing   ":  "leading-trailing",
		"!!!":                     "",
		"Zurich (main)":           "zurich-main",
		strings.Repeat("x", 100):  strings.Repeat("x", 63),
	}
	for in, want := range cases {
		t.Run(in, func(t *testing.T) {
			got := Slugify(in)
			if got != want {
				t.Errorf("Slugify(%q) = %q, want %q", in, got, want)
			}
		})
	}
}
