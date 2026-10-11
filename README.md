# manage

An application to run an event — a conference, a free software meeting — in
one place instead of data copied from one platform to another. It brings
together what used to be two applications:

- **partners**: sponsors follow their partnership and the organizers manage
  the sponsor pipeline (described below);
- **volunteers**: volunteers join poles (teams) and sign up on shifts, pole
  leads and the coordination of each edition run the planning. This
  repository keeps the history of both former applications.

One person is one e-mail address, whatever they come for: a partner contact
who also volunteers logs in once and goes from one space to the other from
their menu.

The event name, website, contact address and logo are configurable, so it
can be used for another event.

It is a single Go binary (templates, CSS and migrations are embedded) backed
by SQLite, with Tailwind CSS compiled at build time. No Node.js, no CGO.

## Features

**Partners** (`/partners/`, password-less login by magic link):

- progress bar: quote received → quote signed → invoice received → invoice
  paid → details complete → logo provided → attendees registered, plus a
  "what's left to do" list with deadlines
- sponsorship level shown as a medal (color per level, size by rank), badges
  of past and current partnerships ("Gold sponsor 2025"), and the perks
  included, ticked off by the staff as they are delivered
- their staff contact (name, avatar, email)
- quote, invoice, sponsorship brochure and sponsor guide (PDF)
- organization details: billing (legal name, address, SIRET, VAT, purchase
  order number) and communication (website, FR/EN description, social links)
- logo upload (SVG/PNG/JPEG/WebP, optional dark-background variant, history)
- attendees (booth staff / visitors, dietary requirements), capped per level
  and locked after the deadline
- job offers (link and/or text), published on the public `/jobs` page
- social media announcements: planned date, then link once published
- managers: any manager can invite colleagues; one email can manage several
  organizations
- payment instructions (bank details, legal mention) next to the invoice
- own profile (name, email language)
- French and English interface and emails
- system / light / dark theme (system by default), for staff too

**Volunteers** (`/volunteers/`, same login; a newcomer's account is created
when they first use their link, while an edition takes volunteers):

- an invitation link names the pole and its leads on the login page; a
  newcomer gives their name, then lands where they were heading
- poles with four access modes: public, private (invitation link with a
  key), on invitation (leads add people), restricted (only the coordination
  adds people, hidden from others); a default pole holds the shifts of no
  team in particular and includes everyone
- one-click sign-up (a public pole is joined on the way), overlap warning,
  waiting list on full shifts with an e-mail when a place frees up, task
  checklists ticked on the day; shift cards are updated in place with htmx
- one planning for everyone, whose content follows the role: a volunteer
  sees the shifts of their poles and of the open ones with who is on them
  (no contact details), and signs up only themselves; a pole lead also sees
  phone numbers and edit links on their poles; the coordination sees every
  pole. Views "all", "mine" and "to fill", filters by pole and day, today
  first during the edition, a print layout that is the roster for the day
  (boxes to tick, blank lines for free places), and a CSV of sign-ups
- personal calendar subscription link that follows changes, returning
  volunteers offered last year's poles
- one page per pole: its shifts for everyone; for its leads, tabs for
  members (search, add by address), messages, settings (picture, places,
  freeze date) and history, and a slot generator (days, hours per day,
  places, with a preview)
- the edition, for its coordinators: coverage of every pole, volunteer list
  with CSV export, messages to all, volunteer status (draft, open, frozen,
  archived), coordinators, an edition prepared from another
- nobody loses a shift silently: moving or deleting a shift, taking someone
  off or out of a pole e-mails them; a withdrawal less than 48 hours ahead
  warns the leads; a recap of the next day's shifts goes out every evening
- profile: name, phone (shared with volunteers only if a lead chooses to),
  photo, or the Gravatar of the address fetched by the server so browsers
  never contact gravatar.com; a volunteer can stop volunteering, which
  deletes their account unless they are also a partner contact or staff

**Staff** (`/admin/`, OpenID Connect):

- pipeline per edition: prospect → contacted → negotiating → confirmed /
  declined, amounts confirmed / signed / paid, slots left per level, next
  follow-up date, staff owner with a "my partners" filter
- one file per partner: breadcrumb, badges, one tab per edition and an
  "Organisation" tab (details, managers, logos, job offers, activity)
- "+ Edition" on the file: renews from the latest edition (same level by
  name, same owner, status "contacted"), or starts a prospect
- staff profile (display name, avatar)
- editions, levels (medal color, price, VAT regime: excluding VAT, including VAT or not
  applicable, max partners, max attendees), perks and the level × perk
  matrix; they can be copied from another edition (on creation or later,
  merged by name without overwriting)
- partnership page: status, level, negotiated price overriding the level
  price, dates, PDF uploads (managers notified by email), perks checklist,
  attendees, internal notes
- managers: add, edit name/email/language (changing the email logs the
  manager out), send a login link, remove
- social media posts per partner (network, planned date, link once
  published); managers can be emailed when a post goes live
- "view as partner" on the edition being looked at, coming back to the same
  tab; activity log; attendee CSV export
- email notification to the staff when a partner changes something
- automatic reminders 14 and 3 days before the logo and attendee deadlines

A partnership is only visible to the partner once its status is
*negotiating* or *confirmed*.

URLs and forms never expose integer ids, so identifiers cannot be guessed by
counting: editions and organizations are addressed by slug
(`/admin/events/example-conf-2026`), a partnership by organization and
edition (`/admin/organizations/acme/editions/example-conf-2026`), and
other rows (levels, perks, managers, attendees, job offers, posts, staff,
shifts, tasks, places, messages, people) by a random UUID; poles are addressed
by edition and slug (`/volunteers/example-conf-2026/buvette`). Tests
crawl the pages to enforce it.

## Development

Requirements: Go (see `go.mod`) and `make`. The Tailwind standalone CLI is
downloaded into `bin/` on first build.

```sh
make dev            # builds the CSS and starts on http://localhost:8080 in dev mode
make css-watch      # in another terminal, rebuilds the CSS on template changes
make test
```

The test suite covers the full partner, volunteer and staff flows, access control,
the OIDC callback (against a fake provider), upload validation, reminders,
migrations, the export format and French/English catalog parity.

In dev mode (`MANAGE_DEV=1`):

- emails are printed to the log instead of being sent (unless SMTP is set),
  so magic links can be copied from the terminal;
- without OIDC, `/admin/login` offers a password-less form (restricted to
  `MANAGE_ADMIN_EMAILS` when set). It is only enabled when
  `MANAGE_BASE_URL` is a loopback address, so a forgotten `MANAGE_DEV`
  cannot open the admin area on a public server.

Typical first steps: log in at `/admin/login`, create an edition with its
levels and perks, create an organization with a first manager, set the
partnership to *negotiating* or *confirmed*, then use "view as partner" or
the invitation link from the log.

### Third-party assets

[pdf.js](https://mozilla.github.io/pdf.js/) (Apache-2.0) is vendored in
`web/static/vendor/pdfjs/` to render the first page of PDFs chosen in upload
dialogs; it is loaded only when a PDF is picked. `make vendor-pdfjs` refreshes
it (version in the Makefile, npm integrity hash checked).

[htmx](https://htmx.org) (0BSD) is vendored in `web/static/vendor/htmx/` and
loaded on the volunteer pages only. `make vendor-htmx` refreshes it the same
way.

## Configuration

Everything is configured with environment variables; see
[`.env.example`](.env.example). The main ones:

| Variable | Description |
| --- | --- |
| `MANAGE_BASE_URL` | Public URL, used in emails and logo URLs |
| `MANAGE_SITE_NAME` | Event name in pages and emails (default: « Mon événement ») |
| `MANAGE_SITE_URL` | Event website, linked in the footer |
| `MANAGE_CONTACT_EMAIL` | Contact address shown to partners, default sender |
| `MANAGE_VOLUNTEER_CONTACT_EMAIL` | Contact address shown to volunteers and set as Reply-To of their e-mails (default: `MANAGE_CONTACT_EMAIL`) |
| `MANAGE_SITE_LOGO` | Optional path to the event logo (SVG, PNG, JPEG, WebP), header and favicon; initials of the name otherwise |
| `MANAGE_DATA_DIR` | Directory for `manage.db` and uploaded files |
| `MANAGE_SMTP_*`, `MANAGE_MAIL_FROM` | Outgoing email |
| `MANAGE_STAFF_NOTIFY_EMAILS` | Staff addresses notified of partner changes |
| `MANAGE_OIDC_ISSUER`, `_CLIENT_ID`, `_CLIENT_SECRET` | Staff SSO; the redirect URL is `$MANAGE_BASE_URL/admin/callback` |
| `MANAGE_ADMIN_EMAILS`, `MANAGE_ADMIN_GROUP` | Who may log in as staff |
| `MANAGE_EXPORT_TOKEN` | Token for the JSON export, at least 32 characters (`openssl rand -hex 32`); empty disables token access |
| `MANAGE_TRUSTED_PROXIES` | Peers whose `X-Forwarded-For` is believed, to rate-limit login requests per client: addresses or CIDR ranges, `private` (default: loopback and private ranges) or `none` |
| `MANAGE_VOLUNTEER_REMINDER_HOUR` | Hour (event time, 0–23) at which volunteers get the recap of their slots of the next day (default: 18) |
| `MANAGE_GRAVATAR` | `1` (default) looks missing profile pictures up on gravatar.com, from the server; `0` never does |

## JSON export for the website

```sh
curl -H "Authorization: Bearer $MANAGE_EXPORT_TOKEN" \
  "https://manage.example.org/api/partners.json?event=example-conf-2026"
# or, on the server:
manage export -event example-conf-2026 -o partners.json
```

Without `event`, the current edition is exported. Only confirmed partners
are included, grouped by level (highest first):

```json
{
  "generated_at": "2026-10-01T12:00:00Z",
  "event": { "slug": "example-conf-2026", "name": "Example Conf 2026", "year": 2026 },
  "levels": [
    {
      "name": "Or",
      "position": 2,
      "partners": [
        {
          "name": "Acme",
          "slug": "acme",
          "website": "https://acme.example",
          "description": { "fr": "…", "en": "…" },
          "logo": "https://manage.example.org/logos/3f2a….svg",
          "logo_dark": "https://manage.example.org/logos/9b1c….svg",
          "social": { "linkedin": "https://www.linkedin.com/company/acme", "x": "@acme" },
          "hiring": true,
          "jobs_url": "https://manage.example.org/jobs#acme"
        }
      ]
    }
  ]
}
```

Logos are served publicly under `/logos/` (current logos of organizations
that are or were confirmed partners; prospects' logos are only visible to the
staff and the organization itself). Other files (quotes, invoices,
brochures) require authentication.

The format is pinned by `internal/export/testdata/export.golden.json`. After
an intentional change, run `go test ./internal/export -update` and review the
diff.

## Deployment

```sh
docker build -t manage .
docker run -d --name manage -p 8080:8080 -v manage-data:/data --env-file .env manage
```

To use a logo, mount it read-only and point to it:

```sh
docker run … -v ./logo.svg:/brand/logo.svg:ro -e MANAGE_SITE_LOGO=/brand/logo.svg manage
```

The logo is read at startup; restart the container after changing it. Staff
avatars are set by each staff member on their profile page (`/admin/profile`).

Dates and times are shown, and volunteer reminders sent, in the Europe/Paris
time zone.

Put it behind a TLS reverse proxy. Cookies get the `Secure` flag when
`MANAGE_BASE_URL` starts with `https://`. Back up the `/data` volume
(SQLite database in WAL mode plus the `files/` directory). `manage backup
[path]` writes a consistent copy of the database while the server runs, for
a cron job during the event:

```sh
docker exec manage /manage backup /data/backup-$(date +%F).db
```

## Layout

```
cmd/manage/          entry point: serve, export, backup
internal/config/     environment configuration
internal/store/      SQLite schema (migrations/), models and queries
internal/server/     HTTP handlers: public, partner, volunteer, admin, OIDC, reminders
internal/export/     JSON export
internal/files/      uploaded file storage and validation
internal/gravatar/   server-side Gravatar lookup for profile pictures
internal/ratelimit/  in-memory rate limiter (login requests)
internal/i18n/       French / English strings
internal/mail/       SMTP sender
web/templates/       html/template pages
web/static/          Tailwind input, compiled CSS, small JS
```

## Licence

[Apache License 2.0](LICENSE).
