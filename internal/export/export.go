// Package export builds the JSON document consumed by the event website
// build: confirmed partners grouped by level.
package export

import (
	"context"
	"time"

	"manage/internal/store"
)

type Document struct {
	GeneratedAt time.Time `json:"generated_at"`
	Event       Event     `json:"event"`
	Levels      []Level   `json:"levels"`
}

type Event struct {
	Slug string `json:"slug"`
	Name string `json:"name"`
	Year int    `json:"year"`
}

type Level struct {
	Name     string    `json:"name"`
	Position int       `json:"position"`
	Partners []Partner `json:"partners"`
}

type Partner struct {
	Name        string            `json:"name"`
	Slug        string            `json:"slug"`
	Website     string            `json:"website,omitempty"`
	Description map[string]string `json:"description"`
	Logo        string            `json:"logo,omitempty"`
	LogoDark    string            `json:"logo_dark,omitempty"`
	Social      map[string]string `json:"social,omitempty"`
	Hiring      bool              `json:"hiring"`
	JobsURL     string            `json:"jobs_url,omitempty"`
}

// Build exports the confirmed partners of an event. Partners without a level
// are listed last under an empty level name.
func Build(ctx context.Context, st *store.Store, baseURL string, e *store.Event) (*Document, error) {
	doc := &Document{
		GeneratedAt: time.Now().UTC().Truncate(time.Second),
		Event:       Event{Slug: e.Slug, Name: e.Name, Year: e.Year},
		Levels:      []Level{},
	}

	levels, err := st.ListLevels(ctx, e.ID)
	if err != nil {
		return nil, err
	}

	index := map[int64]int{}
	for i, l := range levels {
		doc.Levels = append(doc.Levels, Level{Name: l.Name, Position: l.Position, Partners: []Partner{}})
		index[l.ID] = i
	}

	partnerships, err := st.ListEventPartnerships(ctx, e.ID)
	if err != nil {
		return nil, err
	}

	var noLevel []Partner

	publicJobs, err := st.ListPublicJobs(ctx, e.ID)
	if err != nil {
		return nil, err
	}

	hiring := map[int64]bool{}
	for _, j := range publicJobs {
		hiring[j.Org.ID] = true
	}

	for _, p := range partnerships {
		if p.Status != store.StatusConfirmed {
			continue
		}

		org, err := st.GetOrganization(ctx, p.OrgID)
		if err != nil {
			return nil, err
		}

		partner := Partner{
			Name:        org.Name,
			Slug:        org.Slug,
			Website:     org.Website,
			Description: map[string]string{"fr": org.Description("fr"), "en": org.Description("en")},
			Social:      map[string]string{},
			Hiring:      hiring[org.ID],
		}

		if org.LogoFile != "" {
			partner.Logo = baseURL + "/logos/" + org.LogoFile
		}

		if org.LogoDarkFile != "" {
			partner.LogoDark = baseURL + "/logos/" + org.LogoDarkFile
		}

		for k, v := range map[string]string{"linkedin": org.LinkedIn, "mastodon": org.Mastodon, "bluesky": org.Bluesky, "x": org.X} {
			if v != "" {
				partner.Social[k] = v
			}
		}

		if partner.Hiring {
			partner.JobsURL = baseURL + "/jobs#" + org.Slug
		}

		if p.LevelID != nil {
			if i, ok := index[*p.LevelID]; ok {
				doc.Levels[i].Partners = append(doc.Levels[i].Partners, partner)

				continue
			}
		}

		noLevel = append(noLevel, partner)
	}

	for i := range doc.Levels {
		sortPartners(doc.Levels[i].Partners)
	}

	if len(noLevel) > 0 {
		sortPartners(noLevel)
		doc.Levels = append(doc.Levels, Level{Name: "", Position: -1, Partners: noLevel})
	}

	return doc, nil
}
