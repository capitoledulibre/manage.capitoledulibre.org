# Example Conf — manage

The application the organizers run Example Conf with: one place for the
whole event instead of data copied from one platform to another. It brings
together what used to be two applications:

- **partners**: sponsors follow their partnership and the organizers manage
  the sponsor pipeline (described below);
- **volunteers**: volunteers join poles and sign up on shifts, pole managers
  and the event coordination run the planning. Being merged in from
  benevoles.example.org, whose history this repository keeps.

The event name, website, contact address and logo are configurable, so it
can be used for another event.

It is a single Go binary (templates, CSS and migrations are embedded) backed
by SQLite, with Tailwind CSS compiled at build time. No Node.js, no CGO.

## Features

**Partners** (`/p/`, password-less login by magic link):

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
other rows (levels, perks, managers, attendees, job offers, posts, staff) by a
random UUID. A test crawls the pages to enforce it.

## Development

Requirements: Go (see `go.mod`) and `make`. The Tailwind standalone CLI is
downloaded into `bin/` on first build.

```sh
make dev            # builds the CSS and starts on http://localhost:8080 in dev mode
make css-watch      # in another terminal, rebuilds the CSS on template changes
make test
```

The test suite covers the full partner and staff flows, access control,
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

## Configuration

Everything is configured with environment variables; see
[`.env.example`](.env.example). The main ones:

| Variable | Description |
| --- | --- |
| `MANAGE_BASE_URL` | Public URL, used in emails and logo URLs |
| `MANAGE_SITE_NAME` | Event name in pages and emails (default: Example Conf) |
| `MANAGE_SITE_URL` | Event website, linked in the footer |
| `MANAGE_CONTACT_EMAIL` | Contact address shown to partners, default sender |
| `MANAGE_SITE_LOGO` | Optional path to the event logo (SVG, PNG, JPEG, WebP), header and favicon; initials of the name otherwise |
| `MANAGE_DATA_DIR` | Directory for `partners.db` and uploaded files |
| `MANAGE_SMTP_*`, `MANAGE_MAIL_FROM` | Outgoing email |
| `MANAGE_STAFF_NOTIFY_EMAILS` | Staff addresses notified of partner changes |
| `MANAGE_OIDC_ISSUER`, `_CLIENT_ID`, `_CLIENT_SECRET` | Staff SSO; the redirect URL is `$MANAGE_BASE_URL/admin/callback` |
| `MANAGE_ADMIN_EMAILS`, `MANAGE_ADMIN_GROUP` | Who may log in as staff |
| `MANAGE_EXPORT_TOKEN` | Token for the JSON export, at least 32 characters (`openssl rand -hex 32`); empty disables token access |

## JSON export for the website

```sh
curl -H "Authorization: Bearer $MANAGE_EXPORT_TOKEN" \
  "https://partners.example.org/api/partners.json?event=example-conf-2026"
# or, on the server:
partners export -event example-conf-2026 -o partners.json
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
          "logo": "https://partners.example.org/logos/3f2a….svg",
          "logo_dark": "https://partners.example.org/logos/9b1c….svg",
          "social": { "linkedin": "https://www.linkedin.com/company/acme", "x": "@acme" },
          "hiring": true,
          "jobs_url": "https://partners.example.org/jobs#acme"
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

## Importing the previous Django data

The previous application (Django) stored editions, levels, perks and
partners. They can be imported once:

```sh
partners import-django -dry-run /path/to/old/db.sqlite3   # show what would be imported
partners import-django /path/to/old/db.sqlite3
```

Partnerships are imported as confirmed. Organizations are matched by name
regardless of case, spaces and punctuation ("DataBene" reuses "Data Bene"),
the organizer of an edition is skipped and the placeholder city "Somewhere" is
dropped. Running it again creates nothing.

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

Put it behind a TLS reverse proxy. Cookies get the `Secure` flag when
`MANAGE_BASE_URL` starts with `https://`. Back up the `/data` volume
(SQLite database in WAL mode plus the `files/` directory).

## Layout

```
cmd/manage/          entry point: serve, export
internal/config/     environment configuration
internal/store/      SQLite schema (migrations/), models and queries
internal/server/     HTTP handlers: public, partner, admin, OIDC, reminders
internal/export/     JSON export
internal/files/      uploaded file storage and validation
internal/i18n/       French / English strings
internal/mail/       SMTP sender
web/templates/       html/template pages
web/static/          Tailwind input, compiled CSS, small JS
```
