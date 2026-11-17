# manage — the whole event

Status: **draft**, 2026-11-17. Not validated: the decisions below are
proposals, the lots an order to discuss.

`docs/platform.md` records what manage is today: the partners' space and
the volunteers' planning, on one person record. This plan covers what
turns it into the tool that runs the whole event — the call for papers
and the program, ticketing, the entrance on the day, badges, exhibitors,
the code of conduct, expenses and feedback — and lets Capitole du Libre
switch off pretalx and pretix.

It follows the conventions of `docs/README.md`: decisions carry a `D`
number in one sequence (the last one claimed before this document is D15,
by `docs/platform.md`), product lots an `L` number (none used before).

---

## 1. Intent

Today an edition of Capitole du Libre lives in several places:

- **pretalx** (`cfp.capitoledulibre.org`) holds the call for papers, the
  review, the schedule and the speakers; its public pages are linked from
  the websites of past editions.
- **pretix** (`tickets.capitoledulibre.org`) holds the tickets.
- **pretix-tools** turns lists into one CSV per category (speakers,
  sponsors, stands, schools and associations, guests) and registers the
  speakers in pretix through its API; **BadgeForge / pybadges** makes
  badge PDFs out of those CSVs.
- **cdl-print** makes the printed matter and the signage from pretalx's
  `schedule.xml` and API, with the stands, the sponsors and the stand
  briefing typed by hand in its `content.py`.
- **manage** holds the partners and the volunteers.

The same person — a speaker who volunteers, a partner's employee who gives
a talk — exists in several of them, and the data is copied from one to
the other by hand or by script.

The goal: **one place for everyone, whatever their part or parts** in the
event. A person logs in once and finds their talk, their ticket, their
shifts, their partnership, their stand and their expenses. The organizers
find every part of the event in one tool, and the tools around it —
cdl-print, the website — read it through one API.

## 2. Explicitly out of scope

Considered on 2026-11-17 and left out. Not to be proposed again unless the
need changes:

- **Payment.** Entrance is free; ticketing takes no payment and has no
  payment gateway.
- **Budget and accounting**, beyond the reimbursements of D40.
- **Organizers' retro-planning** and task management.
- **Suppliers and equipment inventory.**
- **Signage screens** in the rooms. Printed signage stays with cdl-print.
- **A mobile public program** with favourites (the program pages are
  responsive, and the ICS export goes into any calendar).
- **A global satisfaction survey and post-event dashboard.**
- **Social media announcements** of talks and speakers.
- **Attendance certificates and visa invitation letters.**
- **Accessibility needs collected at registration.**
- **A native mobile app.** The check-in is a web app (D34).

Later, not now: **video tracking** (D44).

## 3. Decisions to validate

### Foundations

| # | Subject | Decision | Why |
|---|---|---|---|
| D16 | What manage replaces | manage replaces pretalx and pretix for Capitole du Libre. pretalx is switched off once its past editions are imported and served (D25, D26); pretix once its data is imported (D33) and ticketing has run an edition. | One place per person (D2), extended to every part of the event. |
| D17 | A person and their parts | The person (D2) keeps what does not depend on an edition: name, e-mail, language, photo, and, new, a public biography and links used when they speak. What they do in an edition is a **part**: speaker of a talk, ticket holder, volunteer, partner contact, exhibitor, staff. Parts are the existing rows (pole memberships, managers…) and the new ones (speakers, tickets, stands); no separate "role" table repeats them. A person's home lists their parts in the current edition. | The biography is written once and reused each year; the parts already exist as rows, a copy would drift. |
| D18 | Duplicates | A person may have **secondary addresses**: they log in with any of them, and imports match on all of them. The staff get a **merge** tool: candidates are proposed (same name, an address seen in two imports), a merge moves every part, token and history row to the record kept and makes the other address secondary. The activity log records it. | Imports from pretalx and pretix, plus the volunteers and partners, will bring the same people under two addresses. Without a merge, "one person" breaks at the first import. |
| D19 | Consents and retention | One registry declares each kind of personal data: its purpose, who reads it, how long it is kept, and how it is purged. Consents (video recording and its licence, photos, optional mails) are stored with their date and can be withdrawn by the person. Each module plugs its purge into the edition purge (D8), and the privacy notice is generated from the registry. | GDPR, especially for ticketing, which brings thousands of people; one place to answer "what do you keep about me, and until when". |
| D20 | Teams and permissions | The roles of D6 grow into **teams**: a team belongs to an edition and grants a set of permissions (program committee, ticket desk, code of conduct, grants committee, video, exhibitors…). The existing roles — coordination, pole leads, partner contacts — keep their meaning and rules. Rights stay pure functions in `access` (D6). The activity log covers the sensitive modules (tickets, code of conduct, grants). | Many new restricted roles; one system instead of a special case per module. |
| D21 | One API, versioned | `/api/v1/` serves JSON, designed freely (no compatibility with pretalx's or pretix's APIs). **Public** data — the program, speakers' public profiles, rooms, partners by level with their logos, exhibitors — is readable without a token. Private data requires a token: a person's token (D9), or a **service token** that belongs to no person, for cdl-print and the website build, with read-only scopes. The partners export (D10) moves into it and its token becomes a service token. | cdl-print and the website will be built from manage's data; cdl-print is adapted to the new API rather than manage imitating pretalx. |
| D22 | Telling the website | When published data changes (program, speakers, partners, exhibitors), manage calls a configured **webhook**, debounced, so the website rebuilds without anyone thinking of it. | The site is generated from the API (D21); otherwise it lags behind. |
| D23 | The edition's calendar | An edition has named **milestones** with dates: CFP opening and closing, review end, program publication, speaker confirmation deadline, ticketing opening, badge freeze, the event days. Modules read them (a CFP closed after its date, a reminder sent before a deadline), like the poles' freeze date today. They are copied from one edition to the next, shifted by a year. | Dates that live in one place and drive the modules, instead of being checked by hand. |
| D24 | Venue and rooms | An edition has **rooms**: name, capacity, equipment (filmed, streamed, projector, power), and when each is available. Copied from the previous edition. | The schedule (D29), the workshops' capacity (D36) and the signage all need them; to be modelled before the program. |
| D30 | Mail at volume | Every mail goes through an **outbox** sent by a queue, at a configurable rate, retried on failure. Mails are **essential** (login link, ticket, change to one's talk or shift) or **optional** (news, reminders that can be turned off); optional ones carry an unsubscribe link and header. Bounces mark an address as failing, shown to the staff. The administration checks SPF, DKIM and DMARC of the sending domain. | Ticketing moves from hundreds of recipients to thousands; tickets must not land in spam. |
| D42 | Backups | The server takes **scheduled backups** itself (`manage backup`, D13) with a retention, and a restore test runs in CI. Continuous replication (Litestream) is evaluated before ticketing opens. | Losing the database on the event day — tickets, check-in — would be critical. |

### Program

| # | Subject | Decision | Why |
|---|---|---|---|
| D25 | Importing pretalx | Every past edition is imported from pretalx: submissions, speakers, questions and answers, rooms, the schedule, avatars. **pretalx's codes are kept** (talks, speakers) so their URLs keep working. People are matched by address (D18). The import can be run again until the switch-over. | The history stays, and the old sites keep their links. |
| D26 | pretalx's public URLs | manage serves pretalx's **public** URLs for every imported edition — schedule, talk and speaker pages, `schedule.xml` and the other exports — and `cfp.capitoledulibre.org` **redirects permanently** to the same path on manage's own domain. Only public pages are kept; pretalx's REST API is not reproduced (D21). The edition's slug is pretalx's (`cdl-2026`), and pretalx's paths must not collide with manage's own words (D4). | The websites of past editions keep working once pretalx is switched off. |
| D27 | The call for papers | Per edition: **formats** (talk, workshop, lightning…) with durations, **tracks**, custom **questions** (as pretalx's, e.g. the target audience), co-speakers. Submitting asks to log in by magic link (D3). The **program committee** (a team, D20) reviews with scores and comments; reviewers see each other's scores only once they have scored. The decision (accepted, waiting list, rejected) is mailed when the staff chooses to. | Bring the CFP in-house with what pretalx gives today. |
| D28 | The speaker's journey | An accepted speaker receives their slot and a link to **confirm** they come; those who have not confirmed get reminders, and the staff see who is missing. Their space holds their **logistics** — arrival and departure, hotel, speakers' dinner, technical needs — reusing the volunteers' logistics model, their **consent to recording and its licence** (D19), and the **upload of their slides** after the talk. A thank-you mail closes the edition. | What used to be scripts and pads (2017's confirmation script, slides by mail). |
| D29 | Building and publishing the schedule | The staff place accepted talks into rooms and time slots, with warnings for conflicts (a speaker in two places, a room's capacity, equipment needed). Publishing is explicit and versioned (a change after publication tells the speakers concerned, D15). Exports: **`schedule.xml` (Frab)** for the video team and Giggity/ConfClerk, **ICS**, and the JSON of D21. | The program is the heart of the event; the Frab export is what the video and the apps rely on. |
| D44 | Video (later) | Each filmed talk has a status — recorded, edited, published — and its PeerTube link, published only if the speaker consented (D28). | Second phase. |

### Tickets and the day

| # | Subject | Decision | Why |
|---|---|---|---|
| D31 | Tickets | Tickets are **free**. An edition has **ticket types** with optional quotas — those pretix-tools uses today: attendee, guest, speaker, sponsor, stand, and the schools' and associations' categories (can7, net7, tvn7, photo7, epitech, toulibre…), copied from one edition to the next. A ticket's type is **independent of the person's parts**: a part proposes a default type, the staff can change it, and the rights still come from the parts (D17, D20), never from the ticket. A person registers with their address; the mail that sends the ticket is also their login (D3). Taking part **gives a ticket automatically**: a confirmed speaker, a volunteer, a partner's registered attendee (the existing attendees list), an exhibitor's staff — no more CSV to register them. | The link between the CFP and the ticketing; the one-place promise. A badge or a count by category must not depend on rights, nor rights on a ticket. |
| D32 | The ticket | A ticket is a **PDF with a QR code**, sent by mail and kept in the person's space. The QR code carries the ticket's id and a **signature** (HMAC) so that the check-in can verify it without network. | A ticket that works offline (D34). |
| D33 | Importing pretix | Past orders and attendees are imported from pretix, matched to people (D18), then purged according to D19's retention. | The history of attendance and one record per person. |
| D34 | The entrance | Check-in is a **web app (PWA)** inside manage, for phones: it scans QR codes, works **offline** (the list is downloaded beforehand; scans sync when the network returns; two devices scanning the same ticket offline are flagged at sync, not refused). A printable list is the last resort. A **ticket desk** team (D20) has limited rights: find a person by name or address, see their ticket and its status, resend or reprint it, mark them present, create a ticket on the spot. | The venue's network is not reliable; the desk solves problems without access to the rest. |
| D35 | Badges and print runs | Badges are made for every part (attendee, speaker, volunteer, partner, exhibitor, staff) from the person record. manage records **which badges were sent to print** and in which run, so the next run holds only the new ones and those whose content changed since. Who renders the PDF is an open question (§6). | Several print runs per edition, without reprinting everything or forgetting a late registration. |
| D36 | Workshops | A workshop with limited seats takes **registrations** per session, with a waiting list, reusing the shifts' places and waiting list (one sign-up, a mail when a place frees). Capacity comes from the room (D24) or the workshop. | Seats are limited; the mechanics exist already. |
| D37 | Talking to attendees | The staff send messages to ticket holders (all, by type, by workshop): essential ones (a room change, a cancellation) and optional ones with consent (D30, D19), reusing the volunteers' messages. | A change of room must reach the people concerned. |
| D45 | Assistants (MCP) | The MCP server (D9) is extended to the new modules — program, CFP and review, tickets, exhibitors, reimbursements — each lot adding its tools, through the same services and rights (D6, D20) as the pages. The code of conduct (D39) is never served. | Assistants already help run the volunteers; the same help for the rest of the event, without a second set of rights. |
| D43 | Wallet passes (bonus) | The ticket can be added to **Apple Wallet** (`.pkpass`) and **Google Wallet**. Needs an Apple developer account and certificate and a Google Wallet issuer account. | Comfort; not before D32 works. |

### Around the event

| # | Subject | Decision | Why |
|---|---|---|---|
| D38 | Exhibitors | The associations' village has its own journey, alike the partners' without money: application, review by the exhibitors team, table allocation, power needs, staff registered (D31), set-up information. Exhibitors and their logos are in the public API, so cdl-print stops typing them. | A partner-like journey already proven; removes the hand-typed list. |
| D39 | Code of conduct | Anyone can report an incident, **anonymously** if they wish. Reports are seen only by the code of conduct team (D20), never by the staff as such, never in the API or MCP; follow-up is recorded; retention is short and declared (D19). | Expected of an event today, and the most sensitive data the tool will hold. |
| D40 | Reimbursements | One flow for **speakers' expenses** and **attendance grants**: a request with amounts and receipts, approval **by a member of the staff**, then "paid". Grants first go through an application reviewed by the grants committee; once granted, their reimbursement follows the same flow. An export for the treasurer. | The two share everything after the decision; `attendance-grants` is waiting for it. |
| D41 | Feedback | **Talks**: attendees rate and comment on a talk without an account, from a QR code in the room or the program (an OpenFeedback clone), rate-limited; speakers see their own feedback. **Per part**: after the edition, speakers, volunteers and exhibitors get a short form tied to their record, read by the organizers only. | What the pads (`feedback_*.txt`) were doing, structured; speakers want their ratings. |

## 4. Object model additions

Names indicative, settled by each lot.

- Person: `person_addresses` (secondary addresses), `bio`, `links` (D17, D18).
- `consents`, and a registry of data kinds in code (D19).
- `teams`, `team_members`, permissions in code (D20); `service_tokens` (D21).
- `milestones` (D23); `rooms`, `room_availability` (D24).
- `mail_outbox`, `mail_suppressions` (D30).
- `formats`, `tracks`, `questions`, `submissions`, `submission_speakers`,
  `answers`, `reviews`, `schedule_slots`, `schedule_versions`, `speaker_logistics`,
  `slides` (D25-D29); pretalx codes kept on submissions and speakers.
- `ticket_types`, `tickets`, `checkins` (D31-D34); `badge_runs`,
  `badge_prints` (D35); `workshop_signups` (D36).
- `stands` and their applications (D38); `incidents` (D39);
  `reimbursements`, `grant_applications` (D40); `talk_ratings`,
  `part_feedback` (D41).

## 5. Lot-by-lot plan

The order follows dependencies: the foundations first, then the program —
which also lets pretalx be switched off for past editions early — then
tickets, and what stands around the event last. The CFP opens at the end
of Q1 2027 (§6): phases A and B up to L10 are due by then.

### Phase A — Foundations

**L1 — A person and their parts** (D17, D18). Secondary addresses, login
with any of them, the merge tool and its candidates, biography and links
on the profile, the home listing one's parts.
*Acceptance:* two records of the same person, one a volunteer and one a
partner contact, are merged; the person logs in with either address and
sees both parts.

**L2 — Consents and retention** (D19). The registry, consents with date
and withdrawal, the privacy notice generated from it, the edition purge
calling each module.
*Acceptance:* the privacy notice lists every kind of data with its
retention; withdrawing a consent is recorded and takes effect.

**L3 — Teams and permissions** (D20). Teams per edition, permission sets,
the existing roles expressed in the same rules, the activity log on
sensitive modules.
*Acceptance:* a member of a test team sees exactly the pages its
permissions open; the access tests pass unchanged for existing roles.

**L4 — Calendar and venue** (D23, D24). Milestones and rooms, copied from
the previous edition.
*Acceptance:* a new edition prepared from the previous one has its
milestones shifted by a year and the same rooms.

**L5 — Mail at volume** (D30). Outbox, queue, rate, essential and
optional mails, unsubscribe, bounces, the SPF/DKIM/DMARC check.
*Acceptance:* 5,000 mails queued go out at the configured rate; an
optional mail's unsubscribe stops the next ones, not the essential ones.

**L6 — Backups** (D42). Scheduled backups with retention, a restore test,
a decision on Litestream.
*Acceptance:* a backup from the schedule is restored on a new instance in
CI and serves the same pages.

### Phase B — Program

**L7 — Importing pretalx** (D25). All past editions, codes kept, people
matched, repeatable.
*Acceptance:* every edition of pretalx is in manage with its talks,
speakers, rooms and schedule; running it twice changes nothing.

**L8 — pretalx's public URLs and exports** (D26, part of D29). Public
schedule, talk and speaker pages for imported editions, `schedule.xml`,
ICS.
*Acceptance:* the links of a past edition's website open the right pages
on manage; pretalx can be switched off for past editions.

**L9 — API v1 and the website webhook** (D21, D22). Public and private
endpoints, service tokens, the partners export moved in, the webhook.
cdl-print and the website are then adapted to it (in their repositories).
*Acceptance:* cdl-print builds this year's PDFs from manage's API, the
stands and sponsors included, with nothing typed by hand.

**L10 — The call for papers** (D27). Formats, tracks, questions,
submissions, co-speakers, review, decisions.
*Acceptance:* a submission made by a speaker is reviewed by two members
of the committee and accepted; the speaker gets the mail.

**L11 — The speaker's journey** (D28). Confirmation and reminders,
logistics, recording consent, slides, the thank-you mail.
*Acceptance:* the staff see at a glance who has not confirmed; a speaker
uploads their slides after the talk.

**L12 — Building the schedule** (D29). Placement, conflicts, versioned
publication, change notices.
*Acceptance:* a schedule built in manage exports a `schedule.xml` that
Giggity and the video team's tools read; moving a talk after publication
tells its speakers.

### Phase C — Tickets and the day

**L13 — Tickets** (D31, D32). Types, quotas, registration, the PDF ticket
with its signed QR code, tickets given by one's parts.
*Acceptance:* a confirmed speaker, a volunteer and a partner's attendee
each have a ticket without registering; an attendee registers and
receives theirs.

**L14 — Importing pretix** (D33).
*Acceptance:* past attendees are matched to people and purged per D19.

**L15 — The entrance and the desk** (D34).
*Acceptance:* with the network cut, a phone scans 100 tickets; when it
returns, the scans are synced and a duplicate is flagged; a desk member
finds a person and resends their ticket, and sees nothing else.

**L16 — Badges** (D35).
*Acceptance:* a second print run holds only the badges added or changed
since the first.

**L17 — Workshops** (D36).
*Acceptance:* a workshop full at 20 puts the 21st on the waiting list, and
a withdrawal gives them the seat by mail.

**L18 — Talking to attendees** (D37).
*Acceptance:* a room change mails the registered attendees of that
workshop.

**L19 — Wallet passes** (D43).

### Phase D — Around the event

**L20 — Exhibitors** (D38). **L21 — Code of conduct** (D39).
**L22 — Reimbursements and grants** (D40). **L23 — Feedback** (D41).
Their acceptance is written when they are detailed.

Later: **L24 — Video** (D44).

## 6. Questions

Settled on 2026-11-17:

- **Badge rendering** (D35): decided later, when L16 is detailed — manage
  itself, or manage exposing the next print run and pybadges or cdl-print
  rendering it. The tickets (D32, D43) stay in manage, since they are
  mailed.
- **`cfp.capitoledulibre.org`** redirects to manage (D26).
- **Ticket categories**: the pretix-tools categories are ticket types,
  independent of the person's parts (D31).
- **MCP** is extended to the new modules (D45).
- **Reimbursements** are approved by a member of the staff (D40); the
  ceilings and the treasurer's export are left to L22.
- **The next edition's calendar**: the CFP opens at the end of Q1 2027,
  ticketing later. The order of section 5 is kept: phase A, then phase B
  with the CFP (L10) ready by the end of March 2027, phase C before
  ticketing opens.

No question is open.
