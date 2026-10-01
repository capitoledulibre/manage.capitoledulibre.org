package i18n

import (
	"regexp"
	"slices"
	"testing"
)

var verbRe = regexp.MustCompile(`%(\[\d+\])?[sdv]`)

// TestCatalogParity checks that every message has both languages and that
// both use the same formatting verbs, so fmt arguments line up.
func TestCatalogParity(t *testing.T) {
	for key, m := range messages {
		if m[0] == "" || m[1] == "" {
			t.Errorf("%s: missing French or English text", key)

			continue
		}

		fr, en := verbRe.FindAllString(m[0], -1), verbRe.FindAllString(m[1], -1)
		slices.Sort(fr)
		slices.Sort(en)

		if !slices.Equal(fr, en) {
			t.Errorf("%s: verbs differ: fr=%v en=%v", key, fr, en)
		}
	}
}

func TestFallback(t *testing.T) {
	if got := T("en", "action.save"); got != "Save" {
		t.Errorf("en: %q", got)
	}

	if got := T("de", "action.save"); got != "Enregistrer" {
		t.Errorf("unknown language should fall back to French: %q", got)
	}

	if got := T("fr", "does.not.exist"); got != "does.not.exist" {
		t.Errorf("missing key: %q", got)
	}

	if got := T("fr", "login.sent_body", "a@b.c"); !regexp.MustCompile(`a@b\.c`).MatchString(got) {
		t.Errorf("args not applied: %q", got)
	}
}
