# manage — platform

Status: **record**, written on 2026-11-17 from the code and the history.
It sets down the decisions the partners and volunteers spaces were built
on, so that later plans can rely on them, or supersede them by number.

manage brought together two former applications — the partners' space and
the volunteers' planning — into one, so that an event is run in one place
instead of data copied from one platform to another.

## Decisions

| # | Subject | Decision | Why |
|---|---|---|---|
| D1 | One binary, SQLite | A single Go binary embeds the templates, the CSS and the migrations, over one SQLite database. Tailwind is compiled at build time with the standalone CLI. No Node.js, no CGO. | An association's volunteers run it: one container, one file to back up, nothing to keep in step. |
| D2 | One person, one e-mail | A person is one e-mail address, whatever they come for. A partner contact who also volunteers logs in once and moves between spaces from their menu. | The same people take several parts in the event; one account is what they expect, and the only way to know who is who. |
| D3 | How people log in | People log in without a password, by a magic link sent by e-mail. The staff log in through OpenID Connect (single sign-on); a loopback-only form stands in for it in development. | No password to store, reset or leak; the staff already have a single sign-on. |
| D4 | Editions | An edition (an event's yearly instance) is addressed by its slug, and every page of an edition sits below `/<slug>/`. What spans editions (administration, profile, logins) stays outside. A slug cannot be one of the site's own words. Levels, perks and poles are copied from one edition to the next. | Readable, stable URLs; each year starts from the previous one. |
| D5 | No guessable identifiers | URLs and forms never expose integer ids: editions, organizations and poles by slug, every other row by a random UUID. Tests crawl the pages to enforce it. | Counting ids must not reveal or reach other people's data. |
| D6 | Rights in one place | Package `access` decides who may do what, as pure functions of an actor's roles (staff, coordination of an edition, lead of a pole, contact of a partner) and of what is acted upon — the same rules whether the action comes from a page, a form, the API or MCP. | Written and tested once; no path around them. |
| D7 | Two languages | The interface and every e-mail exist in French and English; each person chooses. Tests check the catalogues' parity. | Speakers and partners are not all French-speaking. |
| D8 | Personal data | A personal data notice (`/privacy`) is built from the configuration and linked from every page. Once an edition is over, the staff can purge its personal data; people left with no part in anything are deleted. Dietary needs are counted, never quoted, outside the coordination. A Gravatar is fetched by the server, on request, so browsers never contact gravatar.com. A volunteer leaving deletes their account unless they have another part. | Collect little, keep it the time it is needed, leak nothing to third parties. |
| D9 | API and assistants | People create personal tokens (90 days, read-only unless they choose otherwise). An MCP server at `/mcp` serves the volunteer side to AI assistants, with OAuth for clients that need it, through the same services and rights (D6) as the pages. | Automation without a second set of rights. |
| D10 | The website export | The website build reads the edition's partners as JSON (`export` package), with an export token or a staff session. Its format is pinned by a golden file. | The site shows the partners without anyone copying them. |
| D11 | E-mails | Every e-mail has a text version and an HTML version made from it. In development e-mails are printed to the log; outside it the server refuses to start without SMTP, so login links never reach a production log. | Readable everywhere; no magic link in a log. |
| D12 | No third party at runtime | Front-end libraries (htmx, pdf.js) are vendored with their integrity checked; pages load nothing from another origin. | Privacy, and a site that works when a CDN does not. |
| D13 | Deployment | Docker Compose, a read-only container without capabilities, listening on loopback behind a TLS reverse proxy; `/healthz` for monitoring. `manage backup` writes a consistent copy of the database without stopping the server. | Small attack surface, simple operations. |
| D14 | Not tied to one event | The event's name, website, contact address and logo are configuration. | Another event can use it; nothing Capitole du Libre-specific is hard-coded. |
| D15 | Nobody loses something silently | Changes that affect a person (a shift moved or deleted, a removal, a late withdrawal) e-mail them or the people in charge; partners' changes notify the staff; reminders go out before deadlines. | People trust the tool when it tells them what changed. |
