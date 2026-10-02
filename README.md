# Bénévoles

A small, self-hosted web application to organise volunteers during an event
(built for [Example Conf](https://conf.example.org), usable by any
event). Volunteers pick their shifts themselves; team leaders see at a glance
what is still missing.

- **Accounts kept across years** – volunteers log in with a magic link sent by
  e-mail, no password. The link opens a one-click confirmation page rather
  than logging in straight away, so the mail security gateways that open
  every link of a message in advance cannot use it up.
- **Events** – one per year, with a lifecycle: draft → open → frozen →
  archived, and an optional automatic freeze date. Events and poles have
  URL slugs: `/example-2026`, `/example-2026/buvette`.
- **Poles** (teams) – each with its own managers, icon, description, optional
  locations (rooms, desks…) and its own freeze date. Four access modes:
  *public* (anyone joins), *private* (join through a generated invitation
  link), *invitation* (managers add members), *restricted* (only
  administrators and the event coordinators add members, the pole is hidden
  from others). Managers, coordinators and administrators can always add or
  remove members directly. A default pole
  created with every event carries the organiser-wide shifts and
  automatically includes every volunteer.
- **Slots** – time range × location × number of volunteers wanted, with a
  shared checklist of tasks and per-slot notices. Series of consecutive slots
  can be generated in one go.
- **Volunteer view** – join one or several poles (with a key when required),
  sign up with one click, get warned about overlapping shifts, see "my
  planning". Each volunteer gets a private subscription link their calendar
  application polls on its own, so a shift moved by a manager updates
  everywhere without re-downloading anything.
- **Waiting list** – a full shift is not a dead end: a volunteer asks to be
  told if a place frees up, and everyone waiting is e-mailed the moment one
  does (first come, first served). Managers see how many people wait on each
  shift, which is the signal that it needs more hands.
- **Nobody loses a shift silently** – deleting a shift, moving it to another
  time or place, taking someone off one or removing them from a pole e-mails
  the people concerned. A volunteer who pulls out less than 48 hours before a
  shift, by hand or by leaving the pole, warns its managers (or the event
  coordinators when it has none). Messages can optionally be pushed by e-mail too,
  per event, per pole or per shift.
- **Whom to call** – the people of a pole see its managers' names and
  e-mail addresses on each shift page and in the day-before reminder. A
  manager's phone number is added only if they choose to share it from their
  profile; otherwise it stays visible to managers only, as the profile says.
- **Coverage dashboard** per pole and a **global planning** grid (every pole,
  every slot, holes highlighted) for organisers, volunteer list with CSV
  export, daily reminder e-mail with the next day's shifts.
- **Year after year** – duplicate an event (poles, locations, managers,
  default task lists, access modes) into a new draft, or copy a single pole.
- **Profile pictures** – volunteers upload a photo, or let the application
  find the avatar attached to their address on [Gravatar](https://gravatar.com).
  The lookup is done by the server and the picture is cached in the upload
  directory, so no volunteer's browser ever contacts a third party: neither
  their IP address nor the e-mail hash of the person on screen leaves the
  server. Without a picture, an account shows its initials. Set
  `GRAVATAR=0` to disable the lookup entirely.
- **Branding** – organisation logo, a logo per edition shown next to its name,
  an emoji icon or an uploaded image per pole.

Single Go binary, SQLite database, server-rendered pages with a little
[htmx](https://htmx.org). No JavaScript build; the only services it talks to
are an SMTP server and, if left enabled, gravatar.com.

## Running it

```sh
go build -o benevoles .
cp .env.example .env      # edit SMTP settings, BASE_URL, etc.
./benevoles serve
```

Administrators, the *staff*, log in through OpenID Connect from the
"Connexion staff" link of the login page: register the application with your
identity provider (redirect URL `BASE_URL/admin/callback`), then set
`OIDC_ISSUER`, `OIDC_CLIENT_ID`, `OIDC_CLIENT_SECRET` and say who is let in
with `ADMIN_EMAILS` and/or `ADMIN_GROUP`. The server refuses to start without
it, unless `DEV_MODE=1`.

Administrator rights come with the session that login opens, for 12 hours,
not with the account: the same address logged in by e-mail link is a
volunteer. Everyone else (volunteers, pole managers, event coordinators) keeps
the e-mail link.

From the **Admin** menu: set the organisation name, create an event, add
poles and their managers, then switch the event to *open* when the shifts
are ready. Administrators can also add any registered user to a pole from
the user list, whatever the pole's access mode.

### Configuration

Environment variables (a `.env` file in the working directory is read too):

| Variable | Default | Purpose |
|---|---|---|
| `ADDR` | `:8080` | Listen address |
| `BASE_URL` | `http://localhost:8080` | Public URL, used in e-mails |
| `DB_PATH` | `benevoles.db` | SQLite file (created on first run) |
| `UPLOAD_DIR` | `uploads` | Directory for uploaded logos, pole images and profile pictures |
| `TIMEZONE` | `Europe/Paris` | Display timezone |
| `COOKIE_SECURE` | unset | Set to `1` behind HTTPS |
| `REMINDER_HOUR` | `18` | Local hour of the daily reminder e-mail |
| `TRUSTED_PROXIES` | `private` | Peers whose `X-Forwarded-For` is believed: addresses or CIDR ranges, `private` (loopback and private ranges), or `none` |
| `GRAVATAR` | `1` | Look profile pictures up on gravatar.com (server side, cached); `0` disables it |
| `SMTP_HOST`, `SMTP_PORT`, `SMTP_USER`, `SMTP_PASS` | `587` | SMTP server (port 465 uses implicit TLS) |
| `MAIL_FROM` | `benevoles@example.org` | Sender address |
| `DEV_MODE` | unset | Set to `1` to print e-mails (and login links) to stdout instead of sending them; without OIDC, also offers a password-less staff login when `BASE_URL` is a loopback address |
| `OIDC_ISSUER`, `OIDC_CLIENT_ID`, `OIDC_CLIENT_SECRET` | unset | OpenID Connect provider for the staff login |
| `ADMIN_EMAILS` | unset | Comma-separated addresses allowed in as staff (in development, restricts the password-less login) |
| `ADMIN_GROUP` | unset | Value of the `groups` claim that lets someone in as staff |

### Commands

```
benevoles serve                 start the web server (default)
benevoles remind [YYYY-MM-DD]   send reminder e-mails for a day (default: tomorrow)
benevoles backup [PATH]         write a consistent copy of the database
```

The server also sends reminders by itself every day at `REMINDER_HOUR`; the
`remind` command is there for cron-based setups and for manual re-sends.

### Docker

```sh
docker build -t benevoles .
docker run -d -p 8080:8080 -v benevoles-data:/data --env-file .env benevoles
```

The database and the uploaded images live in `/data` inside the container.

### Backups

`benevoles backup` writes a consistent single-file snapshot while the server
keeps serving, so it is safe to run from cron during the event:

```sh
benevoles backup                       # ./benevoles-20261121-030000.db
benevoles backup /backups/tonight.db   # or an explicit path
```

It refuses to overwrite an existing file, so a scheduled job cannot silently
replace yesterday's copy. Back up the `uploads/` directory alongside it — the
logos, pole images and profile pictures are not in the database.

Copying the `.db` file by hand is not enough: SQLite runs in WAL mode, so the
`.db`, `.db-wal` and `.db-shm` files would have to be copied together and at
the same instant.

## Roles

| | Volunteer | Pole manager | Event coordinator | Staff (administrator) |
|---|---|---|---|---|
| Join poles, sign up on slots, tick tasks | ✓ | ✓ | ✓ | ✓ |
| Manage slots, locations, notices and members of a pole | | ✓ (own poles) | ✓ (every pole of the event) | ✓ |
| Dashboard, global planning, global notices, volunteer list and CSV export | | | ✓ (own event) | ✓ |
| Create poles, name managers, change the event status and settings | | | ✓ (own event) | ✓ |
| Delete an event, create or duplicate events | | | | ✓ |
| User accounts, site settings, naming the coordinators | | | | ✓ |

The coordinators of an event ("Coordination" in the interface) are named by
an administrator from the event's settings tab, and follow the event when it
is duplicated into the next edition. It is the way to hand an edition over to
its core team without giving them rights over every other edition and every
account.

Volunteers only see events that are *open* or *frozen*. Managers, coordinators and
administrators can prepare a *draft* event. When an event or a pole is
frozen (status or date), volunteers can no longer change their signups;
managers can still edit everything.

### URLs

| Path | Page |
|---|---|
| `/` | the current event |
| `/{event}` | a given event |
| `/{event}/{pole}` | a pole and its slots |
| `/{event}/{pole}/join?key=…` | invitation link of a private pole |
| `/{event}/{pole}/manage` | pole management (managers, admins) |
| `/admin/events/{event}` | event dashboard (admins) |

Slugs are derived from names and can be edited; a few words used by the
application (`admin`, `me`, `slots`, `manage`, …) are reserved.

## Development

```sh
make dev     # DEV_MODE=1 go run . serve
make test    # go vet + go test
```

`make dev` listens on :8080; set `ADDR=:8081 BASE_URL=http://localhost:8081`
in front of it to use another port. The database is `./benevoles.db`, created
on first start: delete it to start over.

Without OIDC, `make dev` offers a password-less staff login at `/admin/login`
("Connexion staff" on the login page): any address opens an administrator
session, or only those of `ADMIN_EMAILS` when it is set. It is only there
when `DEV_MODE=1` and `BASE_URL` is a loopback address, so a `DEV_MODE`
forgotten on a public server does not open the administration.

Login links are printed on the console. Layout: `internal/db` (SQLite access,
migrations embedded from `internal/db/migrations`), `internal/web` (handlers,
templates and static assets embedded in the binary), `internal/mail`,
`internal/reminders`, `internal/upload` (images on disk), `internal/avatar`
(the Gravatar fallback). Templates use Go `html/template`; the UI is in French.

Run the tests with `go test ./...`. The `internal/web` tests drive the whole
application through `httptest` (magic-link login, access modes, signups,
management, uploads, profile pictures, rate limiting), so most changes are
covered end to end. The Gravatar test serves a local stub, so the suite never
reaches the real service.

Login requests are rate limited in memory (5 per e-mail address and 30 per IP
every 15 minutes). The client IP is read from `X-Forwarded-For` only when the
connection comes from one of `TRUSTED_PROXIES`, taking the rightmost address
that is not a proxy: from anyone else the header is ignored, since a client
could otherwise write a new address in it for every attempt. A session lasts
90 days from the last visit. The server stops gracefully on SIGINT/SIGTERM.

## License

Apache License 2.0, see [LICENSE](LICENSE).
