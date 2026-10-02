package web

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/png"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"benevoles/internal/avatar"
	"benevoles/internal/config"
	"benevoles/internal/db"
)

// sentMail is one recorded message.
type sentMail struct{ To, Subject, Body string }

// fakeMailer records sent e-mails.
type fakeMailer struct {
	mu    sync.Mutex
	mails []sentMail
	fail  error // returned by Send when set, to play an unreachable server
}

func (f *fakeMailer) Send(to, subject, body string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail != nil {
		return f.fail
	}
	f.mails = append(f.mails, sentMail{To: to, Subject: subject, Body: body})
	return nil
}

func (f *fakeMailer) lastLink(t *testing.T) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.mails) == 0 {
		t.Fatal("no e-mail sent")
	}
	last := f.mails[len(f.mails)-1].Body
	m := regexp.MustCompile(`https?://\S+/auth/\S+`).FindString(last)
	if m == "" {
		t.Fatalf("no login link in e-mail: %q", last)
	}
	return m
}

// reset forgets everything recorded so far, so a test can assert on what one
// specific action sent.
func (f *fakeMailer) reset() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.mails = nil
}

// to returns the messages addressed to one recipient.
func (f *fakeMailer) to(addr string) []sentMail {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []sentMail
	for _, m := range f.mails {
		if m.To == addr {
			out = append(out, m)
		}
	}
	return out
}

type env struct {
	t      *testing.T
	ts     *httptest.Server
	db     *db.DB
	mailer *fakeMailer
	srv    *Server
}

// waitMail blocks until the notifications queued by the last action have been
// attempted; they are sent off the request goroutine on purpose.
func (e *env) waitMail() { e.srv.WaitMail() }

func newEnv(t *testing.T) *env {
	t.Helper()
	dir := t.TempDir()
	d, err := db.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Migrate(); err != nil {
		t.Fatal(err)
	}
	loc, _ := time.LoadLocation("Europe/Paris")
	cfg := config.Config{BaseURL: "http://example.test", Timezone: loc, UploadDir: filepath.Join(dir, "uploads"), DevMode: true}
	mailer := &fakeMailer{}
	srv, err := New(d, cfg, mailer)
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(func() { ts.Close(); srv.WaitMail(); d.Close() })
	return &env{t: t, ts: ts, db: d, mailer: mailer, srv: srv}
}

// client is a browser-like HTTP client with its own cookie jar.
type client struct {
	env  *env
	http *http.Client
	hx   bool
}

func (e *env) client() *client {
	jar, _ := cookiejar.New(nil)
	return &client{env: e, http: &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}

// login performs the magic-link flow for email and sets the display name.
func (e *env) login(email, name string) *client {
	c := e.client()
	c.post("/login", 200, url.Values{"email": {email}})
	link := e.mailer.lastLink(e.t)
	path := link[strings.Index(link, "/auth/"):]
	c.followLoginLink(path)
	if name != "" {
		c.post("/profile", 303, url.Values{"name": {name}})
	}
	return c
}

// followLoginLink opens an e-mailed login link and clicks the button of the
// page it lands on, as a volunteer does.
func (c *client) followLoginLink(path string) {
	c.env.t.Helper()
	want(c.env.t, c.get(path, 200), "Me connecter")
	u, _ := url.Parse(path)
	c.post(u.Path, 303, url.Values{"next": {u.Query().Get("next")}})
}

func (c *client) do(method, path string, want int, form url.Values) string {
	c.env.t.Helper()
	var req *http.Request
	if method == http.MethodGet {
		req, _ = http.NewRequest(method, c.env.ts.URL+path, nil)
	} else {
		req, _ = http.NewRequest(method, c.env.ts.URL+path, strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("Origin", c.env.ts.URL)
	}
	if c.hx {
		req.Header.Set("HX-Request", "true")
	}
	res, err := c.http.Do(req)
	if err != nil {
		c.env.t.Fatal(err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	if res.StatusCode != want {
		c.env.t.Fatalf("%s %s: got %d, want %d\n%s", method, path, res.StatusCode, want, trim(body))
	}
	return string(body)
}

func (c *client) get(path string, want int) string { return c.do(http.MethodGet, path, want, nil) }
func (c *client) post(path string, want int, form url.Values) string {
	return c.do(http.MethodPost, path, want, form)
}

func trim(b []byte) string {
	s := string(b)
	if len(s) > 600 {
		s = s[:600] + "…"
	}
	return s
}

func want(t *testing.T, body, substr string) {
	t.Helper()
	if !strings.Contains(body, substr) {
		t.Fatalf("expected %q in response:\n%s", substr, trim([]byte(body)))
	}
}

func wantNot(t *testing.T, body, substr string) {
	t.Helper()
	if strings.Contains(body, substr) {
		t.Fatalf("did not expect %q in response", substr)
	}
}

func TestFullFlow(t *testing.T) {
	e := newEnv(t)

	// Administrator sets up an event with a private pole and slots.
	admin := e.login("admin@example.test", "Alice Admin")
	want(t, admin.get("/", 200), "Aucun événement")
	admin.get("/admin", 403)
	e.db.SetAdmin(1, true)
	admin.post("/admin/events", 303, url.Values{"name": {"Test 2026"}, "starts_on": {"2026-11-14"}, "ends_on": {"2026-11-15"}})
	want(t, admin.get("/admin/events/test-2026", 200), "Général")
	admin.post("/admin/events/test-2026/poles", 303, url.Values{"name": {"Buvette"}, "access_mode": {"private"}, "manager_email": {"resp@example.test"}})
	admin.post("/admin/events/test-2026/poles", 303, url.Values{"name": {"Accueil"}, "access_mode": {"invitation"}, "manager_email": {"resp@example.test"}})
	admin.post("/admin/events/test-2026/poles", 303, url.Values{"name": {"Orga"}, "access_mode": {"restricted"}})
	pole, _ := e.db.GetPole(2)
	if pole.AccessKey == "" {
		t.Fatal("private pole should get a generated key")
	}
	admin.post("/test-2026/buvette/manage", 303, url.Values{"name": {"Buvette"}, "access_mode": {"private"}, "default_tasks": {"Ouvrir le bar\nCompter la caisse"}})
	admin.post("/test-2026/buvette/manage/locations", 303, url.Values{"names": {"Bar A\nBar B"}})
	want(t, admin.get("/test-2026/buvette/manage/slots/new", 200), "Compter la caisse")
	admin.post("/test-2026/buvette/manage/slots", 303, url.Values{
		"starts_at": {"2026-11-14T09:00"}, "ends_at": {"2026-11-14T11:00"}, "capacity": {"2"},
		"repeat_until": {"2026-11-14T17:00"}, "all_locations": {"1"}, "tasks": {"Ouvrir le bar\nCompter la caisse"},
	})
	if strings.Count(admin.get("/test-2026/buvette/manage", 200), "Modifier</a>") != 8 {
		t.Fatal("expected 8 generated slots (4 hours x 2 locations)")
	}
	admin.post("/test-2026/buvette/manage/messages", 303, url.Values{"body": {"RDV 15 min avant"}})
	admin.post("/admin/events/test-2026/messages", 303, url.Values{"body": {"Briefing vendredi 18h"}})
	admin.post("/manage/slots/1/tasks", 303, url.Values{"labels": {"Tache admin"}})
	want(t, admin.get("/manage/slots/1", 200), "Tache admin")

	// A volunteer cannot see a draft event.
	vol := e.login("vol@example.test", "Victor Volontaire")
	want(t, vol.get("/", 200), "Aucun événement")
	vol.get("/test-2026/buvette", 404)

	admin.post("/admin/events/test-2026", 303, url.Values{"name": {"Test 2026"}, "status": {"open"}, "starts_on": {"2026-11-14"}, "ends_on": {"2026-11-15"}})
	home := vol.get("/", 200)
	want(t, home, "Briefing vendredi")
	want(t, home, "Buvette")
	wantNot(t, home, `id="pole-4"`) // restricted pole is hidden

	// Access modes.
	vol.post("/test-2026/buvette/join", 303, url.Values{"key": {"wrong"}})
	want(t, vol.get("/", 200), "pas valable")
	want(t, vol.get("/test-2026/buvette/join?key=wrong", 200), "pas valable")
	want(t, vol.get("/test-2026/buvette/join?key="+pole.AccessKey, 200), "Rejoindre le pôle")
	vol.post("/test-2026/accueil/join", 303, nil)
	want(t, vol.get("/", 200), "sur invitation")
	vol.post("/test-2026/buvette/join", 303, url.Values{"key": {pole.AccessKey}})
	polePage := vol.get("/test-2026/buvette", 200)
	want(t, polePage, "RDV 15 min avant")
	want(t, polePage, "Je m'inscris")

	// Signups through htmx fragments, with overlap detection.
	hx := *vol
	hx.hx = true
	want(t, hx.post("/slots/1/signup", 200, nil), "unsignup")
	want(t, hx.post("/slots/5/signup", 200, nil), "chevauche") // Bar B, same hours
	want(t, hx.post("/slots/5/signup", 200, url.Values{"force": {"1"}}), "unsignup")
	want(t, hx.post("/slots/5/unsignup", 200, nil), "Je m'inscris")
	want(t, vol.get("/me", 200), "Bar A")
	want(t, vol.get("/me.ics", 200), "BEGIN:VEVENT")
	slotPage := vol.get("/slots/1", 200)
	want(t, slotPage, "Victor Volontaire")
	want(t, slotPage, "Ouvrir le bar")
	want(t, hx.post("/tasks/1/toggle", 200, nil), "fait par Victor")
	hx.post("/tasks/3/toggle", 403, nil) // not signed up on slot 2
	vol.get("/test-2026/buvette/manage", 403)
	vol.get("/admin", 403)

	// Non-htmx overlap confirmation page.
	vol.post("/slots/3/signup", 303, url.Values{"back": {"/test-2026/buvette"}})
	want(t, vol.post("/slots/7/signup", 200, url.Values{"back": {"/test-2026/buvette"}}), "Chevauchement")

	// Freeze per pole.
	settings := url.Values{"name": {"Buvette"}, "access_mode": {"private"}, "default_tasks": {"Ouvrir le bar\nCompter la caisse"}}
	settings.Set("freeze_at", "2020-01-01T00:00")
	admin.post("/test-2026/buvette/manage", 303, settings)
	want(t, hx.post("/slots/3/unsignup", 200, nil), "inscriptions sont closes")
	settings.Set("freeze_at", "")
	admin.post("/test-2026/buvette/manage", 303, settings)

	// Pole manager: member management with live search.
	mgr := e.login("resp@example.test", "Rémi Responsable")
	mgr.get("/test-2026/buvette/manage", 200)
	mgr.get("/test-2026/general/manage", 403)
	mgr.post("/test-2026/accueil/manage/members", 303, url.Values{"emails": {"vol@example.test\nnew2@example.test"}})
	members := mgr.get("/test-2026/accueil/manage?tab=members", 200)
	want(t, members, "vol@example.test")
	want(t, members, "new2@example.test")
	mgr.post("/test-2026/accueil/manage/members/3/delete", 303, nil) // Victor is user 3
	wantNot(t, mgr.get("/test-2026/accueil/manage?tab=members", 200), "vol@example.test")
	want(t, mgr.get("/test-2026/accueil/manage/users?q=vic", 200), "Victor")
	wantNot(t, mgr.get("/test-2026/accueil/manage/users?q=v", 200), "Victor")
	mhx := *mgr
	mhx.hx = true
	added := mhx.post("/test-2026/accueil/manage/members", 200, url.Values{"emails": {"vol@example.test"}, "q": {"vic"}})
	want(t, added, "members-table")
	want(t, added, "Personne ajoutée")
	wantNot(t, mgr.get("/test-2026/accueil/manage/users?q=vic", 200), "Ajouter")
	mgr.post("/test-2026/orga/manage/members", 403, url.Values{"emails": {"x@example.test"}})
	admin.post("/test-2026/orga/manage/members", 303, url.Values{"emails": {"vol@example.test"}})
	want(t, vol.get("/", 200), `id="pole-4"`)
	mgr.post("/test-2026/buvette/manage/key", 303, nil)
	if p2, _ := e.db.GetPole(2); p2.AccessKey == pole.AccessKey {
		t.Fatal("key should have been regenerated")
	}
	mgr.post("/manage/slots/1/volunteers", 303, url.Values{"email": {"new@example.test"}})
	want(t, mgr.get("/manage/slots/1", 200), "new@example.test")

	// Admin views, planning, duplication.
	want(t, admin.get("/admin/events/test-2026", 200), "Buvette")
	want(t, admin.get("/admin/events/test-2026/volunteers", 200), "Victor")
	want(t, admin.get("/admin/events/test-2026/volunteers.csv", 200), "vol@example.test")
	want(t, admin.get("/admin/users", 200), "Buvette")
	planning := admin.get("/admin/events/test-2026/planning", 200)
	want(t, planning, "Bar A")
	want(t, planning, `class="chip empty"`)
	admin.post("/admin/events/test-2026/duplicate", 303, url.Values{"name": {"Test 2027"}, "starts_on": {"2027-11-13"}, "ends_on": {"2027-11-14"}})
	dup := admin.get("/admin/events/test-2027", 200)
	want(t, dup, "Buvette")
	want(t, dup, "Rémi Responsable")
	if p, _ := e.db.GetPole(6); p.AccessKey == "" || p.AccessKey == pole.AccessKey || p.DefaultTasks == "" {
		t.Fatalf("duplicated private pole should carry a fresh key and the default tasks: %+v", p)
	}
	if locs, _ := e.db.ListLocations(6); len(locs) != 2 {
		t.Fatalf("locations not duplicated: %d", len(locs))
	}
	if slots, _ := e.db.ListSlots(6, 0); len(slots) != 0 {
		t.Fatal("slots must not be duplicated")
	}
	admin.post("/admin/poles/2/copy", 303, url.Values{"event_id": {"1"}})
	want(t, admin.get("/admin/events/test-2026", 200), "Buvette (copie)")
	admin.get("/nope", 404)
	admin.get("/nope/nope", 404)
	want(t, vol.get("/test-2026", 200), "Buvette")
	vol.get("/test-2027", 404) // draft event hidden from volunteers
	want(t, admin.get("/test-2027", 200), "Test 2027")
	want(t, admin.get("/", 200), `href="/test-2026/buvette/manage"`)
	want(t, admin.get("/admin/users", 200), "Ajouter au pôle")
	admin.post("/admin/users/3/join", 303, url.Values{"pole_id": {"3"}, "back": {"/admin/users"}})
	if !e.db.IsPoleMember(3, 3) {
		t.Fatal("admin should be able to add a user to a pole from the list")
	}
	// Admin registers a volunteer who never signed up, straight into a pole.
	want(t, admin.get("/admin/users", 200), "Enregistrer un bénévole")
	admin.post("/admin/users/new", 303, url.Values{"name": {"Zoé Nouvelle"}, "email": {"zoe@example.test"}, "phone": {"0600"}, "pole_id": {"3"}})
	zoe, err := e.db.GetUserByEmail("zoe@example.test")
	if err != nil || zoe.Name != "Zoé Nouvelle" || zoe.Phone != "0600" || !e.db.IsPoleMember(3, zoe.ID) {
		t.Fatalf("volunteer not registered properly: %+v %v", zoe, err)
	}
	// Re-registering an existing account never overwrites what the person typed.
	admin.post("/admin/users/new", 303, url.Values{"name": {"Autre Nom"}, "email": {"vol@example.test"}})
	if v, _ := e.db.GetUserByEmail("vol@example.test"); v.Name != "Victor Volontaire" {
		t.Fatal("existing name must be kept")
	}
	admin.post("/admin/users/new", 303, url.Values{"name": {"X"}, "email": {"not-an-email"}})
	// Registered from an event without choosing a pole: attached to the event anyway.
	admin.post("/admin/users/new", 303, url.Values{"name": {"Sam Sans-Pôle"}, "email": {"sam@example.test"}, "event_id": {"1"}, "pole_id": {"0"}})
	want(t, admin.get("/admin/events/test-2026/volunteers", 200), "Sam Sans-Pôle")
	// Managers can add "Name <email>" lines.
	mgr.post("/test-2026/accueil/manage/members", 303, url.Values{"emails": {"Yann Dupont <yann@example.test>\nplain@example.test"}})
	if y, _ := e.db.GetUserByEmail("yann@example.test"); y.Name != "Yann Dupont" || !e.db.IsPoleMember(3, y.ID) {
		t.Fatalf("named member not added: %+v", y)
	}

	// Slug edit and initials.
	admin.post("/test-2026/buvette/manage", 303, url.Values{"name": {"Buvette"}, "slug": {"bar"}, "access_mode": {"private"}})
	want(t, admin.get("/test-2026/bar/manage", 200), "Buvette")
	admin.get("/test-2026/buvette", 404)
	want(t, vol.get("/test-2026", 200), `<span class="pole-icon initials">B</span>`)
	admin.post("/admin/events/test-2026", 303, url.Values{"name": {"Test 2026"}, "slug": {"ec"}, "status": {"open"}, "starts_on": {"2026-11-14"}, "ends_on": {"2026-11-15"}})
	want(t, vol.get("/ec/bar", 200), "Buvette")
}

func TestLoginRateLimit(t *testing.T) {
	e := newEnv(t)
	c := e.client()
	for i := 0; i < 3; i++ {
		c.post("/login", 200, url.Values{"email": {"spam@example.test"}})
	}
	want(t, c.post("/login", 429, url.Values{"email": {"spam@example.test"}}), "Trop de demandes")
	// Other addresses are unaffected.
	c.post("/login", 200, url.Values{"email": {"other@example.test"}})
}

func TestUnauthenticatedRedirect(t *testing.T) {
	e := newEnv(t)
	c := e.client()
	c.get("/", 303)
	c.get("/me", 303)
	c.get("/admin", 303)
}

// upload posts a small PNG (or arbitrary bytes) as a multipart file field.
func (c *client) upload(path, field string, data []byte, want int) {
	c.env.t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, _ := mw.CreateFormFile(field, "image.png")
	fw.Write(data)
	mw.Close()
	req, _ := http.NewRequest(http.MethodPost, c.env.ts.URL+path, &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Origin", c.env.ts.URL)
	res, err := c.http.Do(req)
	if err != nil {
		c.env.t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != want {
		c.env.t.Fatalf("upload %s: got %d, want %d", path, res.StatusCode, want)
	}
}

func tinyPNG(t *testing.T) []byte {
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 4, 4))); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// gravatarStub stands in for gravatar.com: it serves a PNG for the addresses
// it knows and 404 for the others, which is what d=404 asks the real service
// to do. It also counts requests, so a test can prove that browsing does not
// hammer it.
type gravatarStub struct {
	ts    *httptest.Server
	mu    sync.Mutex
	calls int
}

func (g *gravatarStub) hits() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.calls
}

// enableGravatar turns the fallback on and points it at a local stub, so the
// suite never reaches out to the real service.
func (e *env) enableGravatar(emails ...string) *gravatarStub {
	e.t.Helper()
	known := map[string]bool{}
	for _, m := range emails {
		known[avatar.Hash(m)] = true
	}
	g := &gravatarStub{}
	g.ts = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		g.mu.Lock()
		g.calls++
		g.mu.Unlock()
		if !known[strings.TrimPrefix(r.URL.Path, "/avatar/")] {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "image/png")
		w.Write(tinyPNG(e.t))
	}))
	e.t.Cleanup(g.ts.Close)
	e.srv.Cfg.Gravatar = true
	e.srv.Avatars.Enabled = true
	e.srv.Avatars.BaseURL = g.ts.URL
	return g
}

// TestProfilePhoto covers the picture a volunteer uploads: it is served by
// this application, it appears next to their name, and removing it is
// remembered rather than undone by the Gravatar fallback.
func TestProfilePhoto(t *testing.T) {
	e := newEnv(t)
	admin := e.login("admin@example.test", "Alice Admin")
	e.db.SetAdmin(1, true)
	admin.post("/admin/events", 303, url.Values{"name": {"Ed"}, "starts_on": {"2026-11-14"}, "ends_on": {"2026-11-15"}})
	admin.post("/admin/events/ed/poles", 303, url.Values{"name": {"Buvette"}, "access_mode": {"public"}})
	admin.post("/admin/events/ed", 303, url.Values{"name": {"Ed"}, "status": {"open"}, "starts_on": {"2026-11-14"}, "ends_on": {"2026-11-15"}})

	vol := e.login("vol@example.test", "Victor Volontaire")
	want(t, vol.get("/profile", 200), ">VV<") // initials until there is a photo

	vol.upload("/profile/photo", "photo", tinyPNG(t), 303)
	u, _ := e.db.GetUserByEmail("vol@example.test")
	if u.Avatar == "" || u.AvatarSource != db.AvatarUpload {
		t.Fatalf("photo not stored: %q / %q", u.Avatar, u.AvatarSource)
	}
	want(t, vol.get("/profile", 200), u.AvatarURL())
	vol.get(u.AvatarURL(), 200)

	// The picture shows up where volunteers are listed.
	vol.post("/ed/buvette/join", 303, nil)
	want(t, admin.get("/ed/buvette/manage?tab=members", 200), u.AvatarURL())
	want(t, admin.get("/admin/events/ed/volunteers", 200), u.AvatarURL())

	// Non-image content is refused and leaves the photo in place.
	vol.upload("/profile/photo", "photo", []byte("<svg onload=alert(1)>"), 303)
	if u2, _ := e.db.GetUserByEmail("vol@example.test"); u2.Avatar != u.Avatar {
		t.Fatal("invalid upload must not replace the photo")
	}

	vol.post("/profile/photo/delete", 303, nil)
	u3, _ := e.db.GetUserByEmail("vol@example.test")
	if u3.Avatar != "" || u3.AvatarSource != db.AvatarOptOut {
		t.Fatalf("photo not removed: %q / %q", u3.Avatar, u3.AvatarSource)
	}
	if _, err := os.Stat(filepath.Join(e.srv.Cfg.UploadDir, u.Avatar)); err == nil {
		t.Fatal("the removed file should be gone from the upload directory")
	}
}

// TestGravatarFallback checks the fallback for accounts with no uploaded
// photo: the server fetches the picture once and serves it itself.
func TestGravatarFallback(t *testing.T) {
	e := newEnv(t)
	g := e.enableGravatar("vol@example.test")

	vol := e.login("vol@example.test", "Victor Volontaire")
	e.srv.Avatars.Wait()
	u, _ := e.db.GetUserByEmail("vol@example.test")
	if u.Avatar == "" || u.AvatarSource != db.AvatarGravatar {
		t.Fatalf("gravatar not cached: %q / %q", u.Avatar, u.AvatarSource)
	}
	// Served from this application, never hotlinked.
	page := vol.get("/profile", 200)
	want(t, page, u.AvatarURL())
	wantNot(t, page, "gravatar.com/avatar")
	vol.get(u.AvatarURL(), 200)

	// Browsing on does not ask the service again.
	hits := g.hits()
	vol.get("/profile", 200)
	vol.get("/me", 200)
	e.srv.Avatars.Wait()
	if g.hits() != hits {
		t.Fatalf("lookup repeated: %d calls, want %d", g.hits(), hits)
	}

	// An address with no Gravatar keeps its initials, and the miss is
	// remembered so it is not retried on every page view.
	e.login("nobody@example.test", "Nadia Nobody")
	e.srv.Avatars.Wait()
	n, _ := e.db.GetUserByEmail("nobody@example.test")
	if n.Avatar != "" {
		t.Fatalf("unknown address should keep its initials, got %q", n.Avatar)
	}
	if n.AvatarCheckedAt == nil {
		t.Fatal("a miss must be recorded")
	}

	// Uploading wins over the fallback; removing the photo opts out for good.
	vol.upload("/profile/photo", "photo", tinyPNG(t), 303)
	vol.post("/profile/photo/delete", 303, nil)
	vol.get("/profile", 200)
	e.srv.Avatars.Wait()
	if u2, _ := e.db.GetUserByEmail("vol@example.test"); u2.Avatar != "" {
		t.Fatalf("an account that opted out must keep its initials, got %q", u2.Avatar)
	}

	// The explicit button brings the Gravatar back.
	vol.post("/profile/photo/gravatar", 303, nil)
	u3, _ := e.db.GetUserByEmail("vol@example.test")
	if u3.Avatar == "" || u3.AvatarSource != db.AvatarGravatar {
		t.Fatalf("explicit lookup failed: %q / %q", u3.Avatar, u3.AvatarSource)
	}
}

func TestUploads(t *testing.T) {
	e := newEnv(t)
	admin := e.login("admin@example.test", "Alice")
	e.db.SetAdmin(1, true)
	admin.post("/admin/events", 303, url.Values{"name": {"Ed"}, "starts_on": {"2026-11-14"}, "ends_on": {"2026-11-15"}})
	admin.post("/admin/events/ed/poles", 303, url.Values{"name": {"Buvette"}, "access_mode": {"public"}})

	admin.upload("/admin/logo", "logo", tinyPNG(t), 303)
	logo := e.db.GetSetting("org_logo", "")
	if !strings.HasSuffix(logo, ".png") {
		t.Fatalf("logo not stored: %q", logo)
	}
	want(t, admin.get("/", 200), "/uploads/"+logo)
	admin.get("/uploads/"+logo, 200)
	admin.get("/uploads/../go.mod", 307) // ServeMux cleans the path instead of serving it

	admin.upload("/ed/buvette/manage/image", "image", tinyPNG(t), 303)
	p, _ := e.db.GetPole(2)
	if p.Image == "" {
		t.Fatal("pole image not stored")
	}
	want(t, admin.get("/ed/buvette", 200), "/uploads/"+p.Image)

	// Non-image content is refused and leaves the previous image in place.
	admin.upload("/ed/buvette/manage/image", "image", []byte("<svg onload=alert(1)>"), 303)
	if p2, _ := e.db.GetPole(2); p2.Image != p.Image {
		t.Fatal("invalid upload must not replace the image")
	}
	admin.post("/ed/buvette/manage/image/delete", 303, nil)
	if p3, _ := e.db.GetPole(2); p3.Image != "" {
		t.Fatal("image not cleared")
	}
	admin.post("/admin/logo/delete", 303, nil)

	// The edition logo is shown next to the event name and survives a
	// duplication, which makes two events share the same file: clearing one
	// of them must not take the picture away from the other.
	admin.upload("/admin/events/ed/image", "image", tinyPNG(t), 303)
	ev, _ := e.db.GetEventBySlug("ed")
	if ev.Image == "" {
		t.Fatal("event logo not stored")
	}
	want(t, admin.get("/admin/events/ed", 200), ev.LogoURL())
	admin.post("/admin/events/ed/duplicate", 303, url.Values{"name": {"Ed 2"}, "starts_on": {"2027-11-13"}, "ends_on": {"2027-11-14"}})
	if next, _ := e.db.GetEventBySlug("ed-2"); next.Image != ev.Image {
		t.Fatalf("duplication should carry the logo over, got %q", next.Image)
	}
	admin.post("/admin/events/ed/image/delete", 303, nil)
	if ev2, _ := e.db.GetEventBySlug("ed"); ev2.Image != "" {
		t.Fatal("event logo not cleared")
	}
	if _, err := os.Stat(filepath.Join(e.srv.Cfg.UploadDir, ev.Image)); err != nil {
		t.Fatal("the file is still used by the duplicated event and must stay")
	}
	admin.get("/uploads/"+ev.Image, 200)

	// Volunteers cannot upload.
	vol := e.login("vol@example.test", "V")
	vol.upload("/ed/buvette/manage/image", "image", tinyPNG(t), 403)
	vol.upload("/admin/logo", "logo", tinyPNG(t), 403)
}

// TestPagesRender walks the pages the main flow does not visit and checks the
// markup added for accessibility and for the sign-up journey: the login page
// naming its destination, the labelled form controls, the paged account list
// and the live region used by the htmx swaps.
func TestPagesRender(t *testing.T) {
	e := newEnv(t)

	// Anonymous: the login page names the event once one is open, and names
	// the pole when the visitor arrives through an invitation link.
	anon := e.client()
	want(t, anon.get("/login", 200), "Espace bénévoles")
	want(t, anon.get("/login", 200), `<label for="login-email">`)
	want(t, anon.get("/login", 200), `class="skip-link"`)

	admin := e.login("admin@example.test", "Admine")
	e.db.SetAdmin(1, true)
	want(t, admin.get("/", 200), "Aucun événement en cours")
	admin.post("/admin/events", 303, url.Values{
		"name": {"Test 2026"}, "starts_on": {"2026-11-21"}, "ends_on": {"2026-11-22"}})
	admin.post("/admin/events/test-2026", 303, url.Values{
		"name": {"Test 2026"}, "slug": {"test-2026"}, "starts_on": {"2026-11-21"},
		"ends_on": {"2026-11-22"}, "status": {"open"}})
	admin.post("/admin/events/test-2026/poles", 303, url.Values{
		"name": {"Buvette"}, "access_mode": {"private"}})

	pole, err := e.db.GetPoleBySlug("test-2026", "buvette")
	if err != nil {
		t.Fatal(err)
	}
	// The invitation link goes through the login wall: the destination has to
	// survive it and be shown, otherwise the volunteer's first screen is a
	// bare form with no context.
	invite := "/test-2026/buvette/join?key=" + pole.AccessKey
	loginWithNext := anon.get("/login?next="+url.QueryEscape(invite), 200)
	want(t, loginWithNext, "invité·e à rejoindre le pôle")
	want(t, loginWithNext, "Buvette")

	// Account list: paged, searched server-side, with the confirmation on the
	// administrator toggle. A second account is needed: the row of the signed-in
	// administrator hides the destructive actions.
	e.login("vol@example.test", "Victor Bénévole")
	users := admin.get("/admin/users", 200)
	want(t, users, `role="search"`)
	want(t, users, "rendre admin")
	want(t, users, "droits d’administration")
	want(t, admin.get("/admin/users?q=zzzz", 200), "Aucun compte ne correspond")
	want(t, admin.get("/admin/users?q=admin", 200), "admin@example.test")

	// Volunteer journey: the slot card carries the sign-up control, and the
	// htmx response carries the live-region update.
	admin.post("/test-2026/buvette/manage/slots", 303, url.Values{
		"starts_at": {"2026-11-21T09:00"}, "ends_at": {"2026-11-21T12:00"}, "capacity": {"2"}})
	admin.post("/test-2026/buvette/join", 303, url.Values{"key": {pole.AccessKey}})
	card := admin.get("/test-2026/buvette", 200)
	want(t, card, "Je m'inscris")
	want(t, card, `aria-label="Filtrer les créneaux"`)

	hx := e.client()
	hx.hx = true
	hx.http.Jar = admin.http.Jar
	frag := hx.post("/slots/1/signup", 200, url.Values{"back": {"/test-2026/buvette"}})
	want(t, frag, `id="announcer"`)
	want(t, frag, "Inscription enregistrée")
	want(t, frag, "Me désinscrire")
	frag = hx.post("/slots/1/unsignup", 200, url.Values{"back": {"/test-2026/buvette"}})
	want(t, frag, "Désinscription enregistrée")
	want(t, frag, "Je m'inscris")

	// Manager screens not covered elsewhere.
	want(t, admin.get("/manage/slots/1", 200), "Inscrire quelqu'un qui n'a pas encore de compte")
	want(t, admin.get("/test-2026/buvette/manage?tab=settings", 200), `for="p-name"`)
	want(t, admin.get("/test-2026/buvette/manage?tab=members", 200), "invite-url")
	want(t, admin.get("/admin/events/test-2026/volunteers", 200), `for="vol-filter"`)
	want(t, admin.get("/admin/events/new", 200), `for="ev-name"`)
	want(t, admin.get("/profile", 200), `for="profile-name"`)

	// An e-mail is still the way to put someone with no account on a slot,
	// but it is now an explicit, separate intent.
	admin.post("/manage/slots/1/volunteers", 303, url.Values{
		"email": {"newbie@example.test"}, "name": {"Nouvelle Recrue"}})
	want(t, admin.get("/manage/slots/1", 200), "newbie@example.test")
	// A name typed into the e-mail field no longer creates an account.
	admin.post("/manage/slots/1/volunteers", 303, url.Values{"email": {"Nouvelle Recrue"}})
	if _, err := e.db.GetUserByEmail("nouvelle recrue"); err == nil {
		t.Fatal("a non-address was accepted as an e-mail and created an account")
	}
}

func TestSafePath(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want bool
	}{
		{"/", true},
		{"/example-2026/buvette", true},
		{"/admin/users?q=a&page=2", true},
		{"", false},
		{"relative", false},
		{"//evil.example", false}, // protocol-relative: off-site
		{`/\evil.example`, false}, // some browsers treat this as "//"
		{"https://evil.example", false},
	} {
		if got := safePath(tc.in); got != tc.want {
			t.Errorf("safePath(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

// setupEvent creates an open event with one public pole and one slot of the
// given capacity, and returns the admin client, the pole and the event slug.
func setupEvent(t *testing.T, e *env, capacity string) (*client, db.Pole, string) {
	t.Helper()
	admin := e.login("chef@example.test", "Chef Orga")
	e.db.SetAdmin(1, true)
	admin.post("/admin/events", 303, url.Values{
		"name": {"Test 2026"}, "starts_on": {"2026-11-21"}, "ends_on": {"2026-11-22"}})
	admin.post("/admin/events/test-2026", 303, url.Values{
		"name": {"Test 2026"}, "slug": {"test-2026"}, "starts_on": {"2026-11-21"},
		"ends_on": {"2026-11-22"}, "status": {"open"}})
	admin.post("/admin/events/test-2026/poles", 303, url.Values{
		"name": {"Buvette"}, "access_mode": {"public"}})
	pole, err := e.db.GetPoleBySlug("test-2026", "buvette")
	if err != nil {
		t.Fatal(err)
	}
	admin.post("/test-2026/buvette/manage/slots", 303, url.Values{
		"starts_at": {"2026-11-21T09:00"}, "ends_at": {"2026-11-21T11:00"}, "capacity": {capacity}})
	return admin, pole, "test-2026"
}

// TestCalendarSubscription covers the feed a calendar application polls on its
// own: a downloaded .ics goes stale the moment a manager moves a slot.
func TestCalendarSubscription(t *testing.T) {
	e := newEnv(t)
	_, _, _ = setupEvent(t, e, "3")
	vol := e.login("vol@example.test", "Vera Bénévole")
	vol.post("/test-2026/buvette/join", 303, nil)
	vol.post("/slots/1/signup", 303, url.Values{"back": {"/test-2026/buvette"}})

	// The secret is minted when the volunteer first opens their planning.
	page := vol.get("/me", 200)
	want(t, page, "/calendar/")
	tok, err := e.db.CalendarToken(2)
	if err != nil {
		t.Fatal(err)
	}

	// The feed answers without any session at all - that is the whole point.
	anon := e.client()
	feed := anon.get("/calendar/"+tok+".ics", 200)
	want(t, feed, "BEGIN:VCALENDAR")
	want(t, feed, "Buvette")
	want(t, feed, "UID:slot-1-2@benevoles") // stable: re-reading updates, never duplicates
	want(t, feed, "REFRESH-INTERVAL")

	anon.get("/calendar/deadbeef.ics", 404)

	// Regenerating stops the calendars subscribed with the old secret.
	vol.post("/me/calendar/reset", 303, nil)
	anon.get("/calendar/"+tok+".ics", 404)
	newTok, _ := e.db.CalendarToken(2)
	if newTok == tok {
		t.Fatal("the calendar secret was not renewed")
	}
	want(t, anon.get("/calendar/"+newTok+".ics", 200), "BEGIN:VCALENDAR")
}

// TestCancellationNotifications: a volunteer must never silently lose a shift.
func TestCancellationNotifications(t *testing.T) {
	e := newEnv(t)
	admin, _, _ := setupEvent(t, e, "3")
	vol := e.login("vol@example.test", "Vera Bénévole")
	vol.post("/test-2026/buvette/join", 303, nil)
	vol.post("/slots/1/signup", 303, url.Values{"back": {"/test-2026/buvette"}})

	// Removed from a slot by a manager.
	e.mailer.reset()
	admin.post("/manage/slots/1/volunteers/2/delete", 303, nil)
	e.waitMail()
	mails := e.mailer.to("vol@example.test")
	if len(mails) != 1 {
		t.Fatalf("expected 1 notification, got %d", len(mails))
	}
	want(t, mails[0].Subject, "Inscription annulée")
	want(t, mails[0].Body, "samedi 21 novembre")

	// A second slot, created before any deletion: SQLite reuses a freed
	// INTEGER PRIMARY KEY, so ids are only stable while nothing is dropped.
	admin.post("/test-2026/buvette/manage/slots", 303, url.Values{
		"starts_at": {"2026-11-22T09:00"}, "ends_at": {"2026-11-22T11:00"}, "capacity": {"2"}})

	// The whole slot disappearing.
	vol.post("/slots/1/signup", 303, url.Values{"back": {"/test-2026/buvette"}})
	e.mailer.reset()
	admin.post("/manage/slots/1/delete", 303, nil)
	e.waitMail()
	mails = e.mailer.to("vol@example.test")
	if len(mails) != 1 {
		t.Fatalf("expected 1 cancellation notice, got %d", len(mails))
	}
	want(t, mails[0].Subject, "Créneau annulé")

	// Removed from the pole, which cancels the slots held there.
	vol.post("/slots/2/signup", 303, url.Values{"back": {"/test-2026/buvette"}})
	e.mailer.reset()
	admin.post("/test-2026/buvette/manage/members/2/delete", 303, nil)
	e.waitMail()
	mails = e.mailer.to("vol@example.test")
	if len(mails) != 1 {
		t.Fatalf("expected 1 pole removal notice, got %d", len(mails))
	}
	want(t, mails[0].Subject, "Retrait du pôle")
	want(t, mails[0].Body, "inscription(s) sur les créneaux de ce pôle ont été annulées")
}

// TestWaitlist: a full slot used to be a dead end.
func TestWaitlist(t *testing.T) {
	e := newEnv(t)
	_, _, _ = setupEvent(t, e, "1") // a single seat, easy to fill
	first := e.login("first@example.test", "Première Inscrite")
	first.post("/test-2026/buvette/join", 303, nil)
	first.post("/slots/1/signup", 303, url.Values{"back": {"/test-2026/buvette"}})

	second := e.login("second@example.test", "Second Candidat")
	second.post("/test-2026/buvette/join", 303, nil)
	page := second.get("/test-2026/buvette", 200)
	want(t, page, "Complet")
	want(t, page, "Me prévenir si une place se libère")

	second.post("/slots/1/waitlist", 303, url.Values{"back": {"/test-2026/buvette"}})
	want(t, second.get("/test-2026/buvette", 200), "Sur liste d'attente")
	if !e.db.OnWaitlist(1, 3) {
		t.Fatal("the waiting list did not record the request")
	}

	// The seat opening is what the whole feature exists for.
	e.mailer.reset()
	first.post("/slots/1/unsignup", 303, url.Values{"back": {"/test-2026/buvette"}})
	e.waitMail()
	mails := e.mailer.to("second@example.test")
	if len(mails) != 1 {
		t.Fatalf("expected 1 free-place notice, got %d", len(mails))
	}
	want(t, mails[0].Subject, "Une place s'est libérée")
	want(t, mails[0].Body, "/slots/1")

	// Taking the place clears the request; nobody is notified twice.
	second.post("/slots/1/signup", 303, url.Values{"back": {"/test-2026/buvette"}})
	if e.db.OnWaitlist(1, 3) {
		t.Fatal("signing up should clear the waiting-list entry")
	}
	// Managers need to see that the slot is undersized.
	admin := e.login("chef@example.test", "")
	want(t, admin.get("/manage/slots/1", 200), "Bénévoles inscrits")
}

// TestMessageNotification: a message nobody comes back to read is a message
// nobody got, so the author can push it by e-mail.
func TestMessageNotification(t *testing.T) {
	e := newEnv(t)
	admin, _, _ := setupEvent(t, e, "3")
	vol := e.login("vol@example.test", "Vera Bénévole")
	vol.post("/test-2026/buvette/join", 303, nil)

	// Without the box ticked, the message is page-only.
	e.mailer.reset()
	admin.post("/test-2026/buvette/manage/messages", 303, url.Values{"body": {"Sans e-mail"}})
	e.waitMail()
	if n := len(e.mailer.to("vol@example.test")); n != 0 {
		t.Fatalf("no e-mail expected without the checkbox, got %d", n)
	}

	e.mailer.reset()
	admin.post("/test-2026/buvette/manage/messages", 303,
		url.Values{"body": {"Rendez-vous 15 minutes avant"}, "notify": {"1"}})
	e.waitMail()
	mails := e.mailer.to("vol@example.test")
	if len(mails) != 1 {
		t.Fatalf("expected 1 message notification, got %d", len(mails))
	}
	want(t, mails[0].Subject, "Message : le pôle")
	want(t, mails[0].Body, "Rendez-vous 15 minutes avant")

	// Event-wide messages reach every volunteer of the event.
	e.mailer.reset()
	admin.post("/admin/events/test-2026/messages", 303,
		url.Values{"body": {"Briefing vendredi 18h"}, "notify": {"1"}})
	e.waitMail()
	if n := len(e.mailer.to("vol@example.test")); n != 1 {
		t.Fatalf("expected the event message to reach the volunteer, got %d", n)
	}
}

// TestOpenSlots covers "where am I needed?": every slot a volunteer can take,
// all poles together, and a signup that joins a public pole on the way.
func TestOpenSlots(t *testing.T) {
	e := newEnv(t)
	admin, _, _ := setupEvent(t, e, "1")
	admin.post("/admin/events/test-2026/poles", 303, url.Values{"name": {"Accueil"}, "access_mode": {"invitation"}})
	admin.post("/test-2026/accueil/manage/slots", 303, url.Values{
		"starts_at": {"2026-11-22T14:00"}, "ends_at": {"2026-11-22T16:00"}, "capacity": {"2"}})

	vol := e.login("vol@example.test", "Vera Bénévole")
	list := vol.get("/slots", 200)
	want(t, list, "Buvette")
	want(t, list, `action="/slots/1/signup"`)
	wantNot(t, list, `action="/slots/2/signup"`) // invitation pole, not a member

	hx := *vol
	hx.hx = true
	frag := hx.post("/slots/1/signup", 200, url.Values{"back": {"/slots"}})
	want(t, frag, "Vous avez rejoint le pôle « Buvette »")
	want(t, frag, "Me désinscrire")
	want(t, frag, `class="slot-title"><span class="pole-icon`) // the card still names its pole
	buvette, _ := e.db.GetPoleBySlug("test-2026", "buvette")
	if !e.db.IsPoleMember(buvette.ID, 2) {
		t.Fatal("signing up on a public pole should join it")
	}
	// Taken, hence no longer listed.
	wantNot(t, vol.get("/slots", 200), `id="slot-1"`)

	// A pole that needs an invitation is not joined through a signup.
	want(t, hx.post("/slots/2/signup", 200, nil), "sur invitation")
	accueil, _ := e.db.GetPoleBySlug("test-2026", "accueil")
	if e.db.IsPoleMember(accueil.ID, 2) {
		t.Fatal("an invitation pole must not be joined by signing up")
	}
}

// TestReturningVolunteer: coming back once a year, a volunteer is offered the
// poles they were in last time.
func TestReturningVolunteer(t *testing.T) {
	e := newEnv(t)
	admin, _, _ := setupEvent(t, e, "3")
	vol := e.login("vol@example.test", "Vera Bénévole")
	vol.post("/test-2026/buvette/join", 303, nil)
	wantNot(t, vol.get("/", 200), "Content de vous revoir")

	admin.post("/admin/events/test-2026/duplicate", 303, url.Values{"name": {"Test 2027"}, "starts_on": {"2027-11-20"}, "ends_on": {"2027-11-21"}})
	admin.post("/admin/events/test-2026", 303, url.Values{"name": {"Test 2026"}, "slug": {"test-2026"}, "status": {"archived"}, "starts_on": {"2026-11-21"}, "ends_on": {"2026-11-22"}})
	admin.post("/admin/events/test-2027", 303, url.Values{"name": {"Test 2027"}, "slug": {"test-2027"}, "status": {"open"}, "starts_on": {"2027-11-20"}, "ends_on": {"2027-11-21"}})

	home := vol.get("/", 200)
	want(t, home, "Content de vous revoir")
	want(t, home, "Pour Test 2026")
	want(t, home, `action="/test-2027/buvette/join"`)
	wantNot(t, home, "Deux étapes") // the generic welcome gives way

	vol.post("/test-2027/buvette/join", 303, nil)
	wantNot(t, vol.get("/", 200), "Content de vous revoir")

	// Someone new has nothing to come back to.
	want(t, e.login("new@example.test", "Nina Nouvelle").get("/", 200), "Deux étapes")
}

// TestWelcome: a new account is asked for its name alone, then sent on to
// where it was heading.
func TestWelcome(t *testing.T) {
	e := newEnv(t)
	setupEvent(t, e, "3")
	c := e.client()
	c.post("/login", 200, url.Values{"email": {"new@example.test"}, "next": {"/test-2026/buvette"}})
	link := e.mailer.lastLink(t)
	c.followLoginLink(link[strings.Index(link, "/auth/"):])

	page := c.get("/welcome?next=/test-2026/buvette", 200)
	want(t, page, `for="welcome-name"`)
	want(t, page, "Continuer vers le pôle Buvette")
	wantNot(t, page, "Photo de profil")

	want(t, c.post("/profile", 200, url.Values{"welcome": {"1"}, "next": {"/test-2026/buvette"}}), "Indiquez votre nom")
	c.post("/profile", 303, url.Values{"welcome": {"1"}, "name": {"Nina Nouvelle"}, "next": {"/test-2026/buvette"}})
	if u, _ := e.db.GetUserByEmail("new@example.test"); u.Name != "Nina Nouvelle" {
		t.Fatalf("name not saved: %+v", u)
	}
	// Once named, the page steps aside.
	c.get("/welcome?next=/", 303)
}

// TestSignupDeadline: volunteers read the date signups close, not "freeze".
func TestSignupDeadline(t *testing.T) {
	e := newEnv(t)
	admin, _, _ := setupEvent(t, e, "3")
	admin.post("/test-2026/buvette/manage", 303, url.Values{"name": {"Buvette"}, "access_mode": {"public"}, "freeze_at": {"2099-11-20T00:00"}})
	vol := e.login("vol@example.test", "Vera Bénévole")
	want(t, vol.get("/test-2026/buvette", 200), "jeudi 19 novembre inclus")
}

// TestEventStaff checks the staff role row by row: everything inside their
// event, nothing site-wide, nothing in another event.
func TestEventStaff(t *testing.T) {
	e := newEnv(t)
	admin, buvette, _ := setupEvent(t, e, "2")
	admin.post("/admin/events/test-2026/poles", 303, url.Values{"name": {"Orga"}, "access_mode": {"restricted"}})
	admin.post("/admin/events", 303, url.Values{"name": {"Autre 2026"}, "starts_on": {"2026-12-05"}, "ends_on": {"2026-12-06"}})
	other, _ := e.db.GetEventBySlug("autre-2026")
	otherPole, _ := e.db.DefaultPole(other.ID)

	staff := e.login("staff@example.test", "Sacha Staff")
	staff.get("/admin/events/test-2026", 403)
	admin.post("/admin/events/test-2026/staff", 303, url.Values{"email": {"staff@example.test"}})
	want(t, admin.get("/admin/events/test-2026?tab=settings", 200), "Sacha Staff")

	// Inside the event: dashboard, planning, volunteers, poles, managers,
	// messages, status.
	want(t, staff.get("/", 200), `href="/admin/events/test-2026"`)
	board := staff.get("/admin/events/test-2026?tab=settings", 200)
	want(t, board, "Staff de l'événement")
	wantNot(t, board, "Ajouter au staff")
	wantNot(t, board, "Dupliquer l'événement")
	wantNot(t, board, "Supprimer l'événement")
	wantNot(t, board, `href="/admin"`)
	staff.get("/admin/events/test-2026/planning", 200)
	staff.get("/admin/events/test-2026/volunteers", 200)
	staff.get("/admin/events/test-2026/volunteers.csv", 200)
	staff.post("/admin/events/test-2026/poles", 303, url.Values{"name": {"Accueil"}, "access_mode": {"public"}})
	staff.get("/test-2026/buvette/manage", 200)
	staff.post(fmt.Sprintf("/admin/poles/%d/managers", buvette.ID), 303, url.Values{"email": {"resp@example.test"}})
	resp, _ := e.db.GetUserByEmail("resp@example.test")
	if !e.db.IsPoleManager(buvette.ID, resp.ID) {
		t.Fatal("staff should name pole managers")
	}
	staff.post("/admin/events/test-2026/messages", 303, url.Values{"body": {"Briefing samedi 8h"}})
	staff.post("/admin/events/test-2026", 303, url.Values{"name": {"Test 2026"}, "slug": {"test-2026"}, "status": {"frozen"}, "starts_on": {"2026-11-21"}, "ends_on": {"2026-11-22"}})
	if ev, _ := e.db.GetEventBySlug("test-2026"); ev.Status != db.StatusFrozen {
		t.Fatal("staff should change the event status")
	}
	// Restricted poles are the administrators' and the staff's.
	want(t, staff.get("/", 200), "Orga")
	staff.post("/test-2026/orga/manage/members", 303, url.Values{"emails": {"vip@example.test"}})
	staff.post("/admin/users/new", 303, url.Values{"name": {"Nouvelle"}, "email": {"nv@example.test"}, "event_id": {"1"}, "back": {"/admin/events/test-2026/volunteers"}})

	// Site-wide and other events stay out of reach.
	staff.get("/admin", 403)
	staff.get("/admin/users", 403)
	staff.post("/admin/events", 403, url.Values{"name": {"X"}, "starts_on": {"2027-01-01"}, "ends_on": {"2027-01-02"}})
	staff.post("/admin/events/test-2026/duplicate", 403, url.Values{"name": {"X"}, "starts_on": {"2027-01-01"}, "ends_on": {"2027-01-02"}})
	staff.post("/admin/events/test-2026/delete", 403, nil)
	staff.post("/admin/events/test-2026/staff", 403, url.Values{"email": {"friend@example.test"}})
	staff.post("/admin/users/1/admin", 403, nil)
	staff.get("/admin/events/autre-2026", 403)
	staff.get(fmt.Sprintf("/autre-2026/%s/manage", otherPole.Slug), 403)
	staff.post(fmt.Sprintf("/admin/poles/%d/copy", buvette.ID), 403, url.Values{"event_id": {fmt.Sprint(other.ID)}})
	staff.post("/admin/users/new", 403, url.Values{"name": {"X"}, "email": {"x@example.test"}})
	staff.post("/admin/users/new", 403, url.Values{"name": {"X"}, "email": {"x@example.test"}, "pole_id": {fmt.Sprint(otherPole.ID)}})

	// The staff follows the event to its next edition.
	admin.post("/admin/events/test-2026/duplicate", 303, url.Values{"name": {"Test 2027"}, "starts_on": {"2027-11-20"}, "ends_on": {"2027-11-21"}})
	staff.get("/admin/events/test-2027", 200)

	// Taken off the staff, back to a volunteer.
	admin.post("/admin/events/test-2026/staff/2/delete", 303, nil)
	staff.get("/admin/events/test-2026", 403)
}

// TestAdminPoleForms posts to the URLs the pages actually render: the pole
// actions used to point at /admin/{event}/{pole}/…, which answered 404.
func TestAdminPoleForms(t *testing.T) {
	e := newEnv(t)
	admin, buvette, _ := setupEvent(t, e, "2")
	board := admin.get("/admin/events/test-2026", 200)
	for _, action := range []string{"copy", "delete"} {
		want(t, board, fmt.Sprintf(`action="/admin/poles/%d/%s"`, buvette.ID, action))
	}
	manage := admin.get("/test-2026/buvette/manage?tab=members", 200)
	want(t, manage, fmt.Sprintf(`action="/admin/poles/%d/managers"`, buvette.ID))
	admin.post(fmt.Sprintf("/admin/poles/%d/managers", buvette.ID), 303, url.Values{"email": {"resp@example.test"}})
	want(t, admin.get("/test-2026/buvette/manage?tab=members", 200), fmt.Sprintf(`action="/admin/poles/%d/managers/`, buvette.ID))

	// The volunteer list offers the event's poles in its selects.
	want(t, admin.get("/admin/events/test-2026/volunteers", 200), fmt.Sprintf(`<option value="%d">Buvette</option>`, buvette.ID))

	// Deleting an event deletes it: the handler used to read an id the route
	// does not carry, and silently kept everything.
	admin.post("/admin/events/test-2026/delete", 303, nil)
	if _, err := e.db.GetEventBySlug("test-2026"); err == nil {
		t.Fatal("the event is still there after deletion")
	}
}

// TestManagerAddressNotShown: a manager who never gave a name used to be
// listed by e-mail address on volunteer pages, and on the login page that an
// invitation link opens to anyone holding it.
func TestManagerAddressNotShown(t *testing.T) {
	e := newEnv(t)
	admin, buvette, _ := setupEvent(t, e, "2")
	admin.post(fmt.Sprintf("/admin/poles/%d/managers", buvette.ID), 303, url.Values{"email": {"ghost@example.test"}})
	vol := e.login("vol@example.test", "Vera Bénévole")
	for _, page := range []string{"/", "/test-2026/buvette", "/test-2026/buvette/join"} {
		wantNot(t, vol.get(page, 200), "ghost@example.test")
	}
	wantNot(t, e.client().get("/login?next="+url.QueryEscape("/test-2026/buvette/join"), 200), "ghost@example.test")

	// Once named, the manager is shown, by name.
	e.login("ghost@example.test", "Gaspard Responsable")
	want(t, vol.get("/test-2026/buvette", 200), "Gaspard Responsable")
}

// TestLoginLinkSurvivesScanner: mail security gateways open every link of an
// incoming message before the recipient does. Their visit must neither log
// them in nor spend the token.
func TestLoginLinkSurvivesScanner(t *testing.T) {
	e := newEnv(t)
	vol := e.client()
	vol.post("/login", 200, url.Values{"email": {"vol@example.test"}})
	link := e.mailer.lastLink(t)
	path := link[strings.Index(link, "/auth/"):]

	scanner := e.client()
	want(t, scanner.get(path, 200), "vol@example.test")
	scanner.get("/", 303) // no session for the scanner

	want(t, vol.get(path, 200), "Me connecter")
	vol.post(path, 303, nil)
	vol.get("/profile", 200)

	// Used once, the link is spent, whatever opens it next.
	want(t, scanner.post(path, 200, nil), "Lien invalide")
	want(t, scanner.get(path, 200), "Lien invalide")
}

// TestLoginMailFailure: an unreachable mail server gets a sentence, not a 500.
func TestLoginMailFailure(t *testing.T) {
	e := newEnv(t)
	e.mailer.mu.Lock()
	e.mailer.fail = errors.New("connection refused")
	e.mailer.mu.Unlock()
	page := e.client().post("/login", 503, url.Values{"email": {"vol@example.test"}})
	want(t, page, "n&#39;a pas pu être envoyé")
	want(t, page, `value="vol@example.test"`) // the address is kept for the retry
}

// TestSlotChangeNotification: a slot that moves e-mails its volunteers; one
// whose title alone changes does not.
func TestSlotChangeNotification(t *testing.T) {
	e := newEnv(t)
	admin, _, _ := setupEvent(t, e, "3")
	vol := e.login("vol@example.test", "Vera Bénévole")
	vol.post("/test-2026/buvette/join", 303, nil)
	vol.post("/slots/1/signup", 303, url.Values{"back": {"/test-2026/buvette"}})

	e.mailer.reset()
	admin.post("/manage/slots/1", 303, url.Values{"title": {"Ouverture"}, "capacity": {"3"},
		"starts_at": {"2026-11-21T09:00"}, "ends_at": {"2026-11-21T11:00"}})
	e.waitMail()
	if n := len(e.mailer.to("vol@example.test")); n != 0 {
		t.Fatalf("a title change sent %d e-mail(s)", n)
	}

	admin.post("/manage/slots/1", 303, url.Values{"title": {"Ouverture"}, "capacity": {"3"},
		"starts_at": {"2026-11-21T10:00"}, "ends_at": {"2026-11-21T12:00"}})
	e.waitMail()
	mails := e.mailer.to("vol@example.test")
	if len(mails) != 1 {
		t.Fatalf("expected 1 change notice, got %d", len(mails))
	}
	want(t, mails[0].Subject, "Créneau modifié")
	want(t, mails[0].Body, "de 09:00 à 11:00")
	want(t, mails[0].Body, "de 10:00 à 12:00")
}

// TestLateWithdrawal: a volunteer pulling out of a slot that starts within
// 48 hours, by hand or by leaving the pole, warns the pole managers.
func TestLateWithdrawal(t *testing.T) {
	e := newEnv(t)
	admin, buvette, _ := setupEvent(t, e, "3") // slot 1, far away
	soon := time.Now().Add(24 * time.Hour).In(e.srv.Cfg.Timezone)
	admin.post("/test-2026/buvette/manage/slots", 303, url.Values{
		"starts_at": {soon.Format("2006-01-02T15:04")}, "ends_at": {soon.Add(2 * time.Hour).Format("2006-01-02T15:04")}, "capacity": {"2"}})
	admin.post(fmt.Sprintf("/admin/poles/%d/managers", buvette.ID), 303, url.Values{"email": {"resp@example.test"}})
	e.login("resp@example.test", "Rémi Responsable")

	vol := e.login("vol@example.test", "Vera Bénévole")
	vol.post("/test-2026/buvette/join", 303, nil)
	vol.post("/slots/1/signup", 303, url.Values{"back": {"/test-2026/buvette"}})
	vol.post("/slots/2/signup", 303, url.Values{"back": {"/test-2026/buvette"}})

	e.mailer.reset()
	vol.post("/slots/1/unsignup", 303, url.Values{"back": {"/test-2026/buvette"}})
	e.waitMail()
	if n := len(e.mailer.to("resp@example.test")); n != 0 {
		t.Fatalf("a withdrawal weeks ahead sent %d e-mail(s) to the manager", n)
	}

	vol.post("/slots/2/unsignup", 303, url.Values{"back": {"/test-2026/buvette"}})
	e.waitMail()
	mails := e.mailer.to("resp@example.test")
	if len(mails) != 1 {
		t.Fatalf("expected 1 late-withdrawal notice, got %d", len(mails))
	}
	want(t, mails[0].Subject, "Désistement de dernière minute")
	want(t, mails[0].Body, "Vera Bénévole")
	want(t, mails[0].Body, "0/2 inscrit·es")

	// Leaving the pole cancels the slot too: same warning.
	vol.post("/slots/2/signup", 303, url.Values{"back": {"/test-2026/buvette"}})
	e.mailer.reset()
	vol.post("/test-2026/buvette/leave", 303, nil)
	e.waitMail()
	if n := len(e.mailer.to("resp@example.test")); n != 1 {
		t.Fatalf("leaving the pole should warn the manager once, got %d", n)
	}
}

// TestManagerContact: the people of a pole see whom to reach; the phone only
// when the manager chose to share it.
func TestManagerContact(t *testing.T) {
	e := newEnv(t)
	admin, buvette, _ := setupEvent(t, e, "3")
	admin.post(fmt.Sprintf("/admin/poles/%d/managers", buvette.ID), 303, url.Values{"email": {"resp@example.test"}})
	resp := e.login("resp@example.test", "")
	resp.post("/profile", 303, url.Values{"name": {"Rémi Responsable"}, "phone": {"0611223344"}})

	outsider := e.login("out@example.test", "Otto Dehors")
	wantNot(t, outsider.get("/slots/1", 200), "resp@example.test")

	vol := e.login("vol@example.test", "Vera Bénévole")
	vol.post("/test-2026/buvette/join", 303, nil)
	page := vol.get("/slots/1", 200)
	want(t, page, "Rémi Responsable")
	want(t, page, `href="mailto:resp@example.test"`)
	wantNot(t, page, "0611223344")

	want(t, resp.get("/profile", 200), `name="share_phone"`)
	wantNot(t, vol.get("/profile", 200), `name="share_phone"`) // not a manager
	resp.post("/profile", 303, url.Values{"name": {"Rémi Responsable"}, "phone": {"0611223344"}, "share_phone_shown": {"1"}, "share_phone": {"1"}})
	want(t, vol.get("/slots/1", 200), `href="tel:0611223344"`)

	// A form without the choice (the welcome page) leaves it alone.
	resp.post("/profile", 303, url.Values{"name": {"Rémi Responsable"}, "phone": {"0611223344"}, "welcome": {"1"}})
	want(t, vol.get("/slots/1", 200), `href="tel:0611223344"`)
}
