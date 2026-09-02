# Bénévoles

A small, self-hosted web application to organise volunteers during an event
(built for [Example Conf](https://conf.example.org), usable by any
event). Volunteers pick their shifts themselves; team leaders see at a glance
what is still missing.

- **Accounts kept across years** – volunteers log in with a magic link sent by
  e-mail, no password.
- **Editions** – one per year, with a lifecycle: draft → open → frozen →
  archived, and an optional automatic freeze date.
- **Poles** (teams) – each with its own managers, icon, description, optional
  locations (rooms, desks…) and its own freeze date. Four access modes:
  *public* (anyone joins), *private* (join through a generated invitation
  link), *invitation* (managers add members), *restricted* (only
  administrators add members, the pole is hidden from others). Managers and
  administrators can always add or remove members directly. A default pole
  created with every edition carries the organiser-wide shifts and
  automatically includes every volunteer.
- **Slots** – time range × location × number of volunteers wanted, with a
  shared checklist of tasks and per-slot notices. Series of consecutive slots
  can be generated in one go.
- **Volunteer view** – join one or several poles (with a key when required),
  sign up with one click, get warned about overlapping shifts, see "my
  planning" and export it to a calendar (.ics).
- **Coverage dashboard** per pole and a **global planning** grid (every pole,
  every slot, holes highlighted) for organisers, volunteer list with CSV
  export, daily reminder e-mail with the next day's shifts.
- **Year after year** – duplicate an edition (poles, locations, managers,
  default task lists, access modes) into a new draft, or copy a single pole.
- **Branding** – organisation logo, an emoji icon or an uploaded image per pole.

Single Go binary, SQLite database, server-rendered pages with a little
[htmx](https://htmx.org). No JavaScript build, no external services besides an
SMTP server.

## Running it

```sh
go build -o benevoles .
cp .env.example .env      # edit SMTP settings, BASE_URL, etc.
./benevoles serve
```

Open the site, log in once with your e-mail, then grant yourself
administrator rights:

```sh
./benevoles make-admin you@example.org
```

From the **Admin** menu: set the organisation name, create an edition, add
poles and their managers, then switch the edition to *open* when the shifts
are ready.

### Configuration

Environment variables (a `.env` file in the working directory is read too):

| Variable | Default | Purpose |
|---|---|---|
| `ADDR` | `:8080` | Listen address |
| `BASE_URL` | `http://localhost:8080` | Public URL, used in e-mails |
| `DB_PATH` | `benevoles.db` | SQLite file (created on first run) |
| `UPLOAD_DIR` | `uploads` | Directory for uploaded logos and pole images |
| `TIMEZONE` | `Europe/Paris` | Display timezone |
| `COOKIE_SECURE` | unset | Set to `1` behind HTTPS |
| `REMINDER_HOUR` | `18` | Local hour of the daily reminder e-mail |
| `SMTP_HOST`, `SMTP_PORT`, `SMTP_USER`, `SMTP_PASS` | `587` | SMTP server (port 465 uses implicit TLS) |
| `MAIL_FROM` | `benevoles@example.org` | Sender address |
| `DEV_MODE` | unset | Set to `1` to print e-mails (and login links) to stdout instead of sending them |

### Commands

```
benevoles serve                 start the web server (default)
benevoles make-admin EMAIL      grant administrator rights
benevoles remind [YYYY-MM-DD]   send reminder e-mails for a day (default: tomorrow)
```

The server also sends reminders by itself every day at `REMINDER_HOUR`; the
`remind` command is there for cron-based setups and for manual re-sends.

### Docker

```sh
docker build -t benevoles .
docker run -d -p 8080:8080 -v benevoles-data:/data --env-file .env benevoles
```

The database and the uploaded images live in `/data` inside the container.
Back them up together: the `.db` file (SQLite in WAL mode: copy the `.db`,
`.db-wal` and `.db-shm` files together, or use
`sqlite3 benevoles.db ".backup copy.db"`) and the `uploads/` directory.

## Roles

| | Volunteer | Pole manager | Administrator |
|---|---|---|---|
| Join poles, sign up on slots, tick tasks | ✓ | ✓ | ✓ |
| Manage slots, locations, notices and members of a pole | | ✓ (own poles) | ✓ |
| Create editions and poles, name managers, global notices, user accounts | | | ✓ |

Volunteers only see editions that are *open* or *frozen*. Managers and
administrators can prepare a *draft* edition. When an edition or a pole is
frozen (status or date), volunteers can no longer change their signups;
managers can still edit everything.

## Development

```sh
DEV_MODE=1 go run . serve
```

Login links are printed on the console. Layout: `internal/db` (SQLite access,
migrations embedded from `internal/db/migrations`), `internal/web` (handlers,
templates and static assets embedded in the binary), `internal/mail`,
`internal/reminders`. Templates use Go `html/template`; the UI is in French.

Run the tests with `go test ./...`. The `internal/web` tests drive the whole
application through `httptest` (magic-link login, access modes, signups,
management, uploads, rate limiting), so most changes are covered end to end.

Login requests are rate limited in memory (3 per e-mail address and 30 per IP
every 15 minutes). Behind a reverse proxy, the client IP is read from
`X-Forwarded-For`. The server stops gracefully on SIGINT/SIGTERM.

## License

Apache License 2.0, see [LICENSE](LICENSE).
