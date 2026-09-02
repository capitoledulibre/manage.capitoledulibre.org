package web

import (
	"bytes"
	"image"
	"image/png"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"benevoles/internal/config"
	"benevoles/internal/db"
)

// fakeMailer records sent e-mails.
type fakeMailer struct {
	mu    sync.Mutex
	mails []string
}

func (f *fakeMailer) Send(to, subject, body string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.mails = append(f.mails, body)
	return nil
}

func (f *fakeMailer) lastLink(t *testing.T) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.mails) == 0 {
		t.Fatal("no e-mail sent")
	}
	m := regexp.MustCompile(`https?://\S+/auth/\S+`).FindString(f.mails[len(f.mails)-1])
	if m == "" {
		t.Fatalf("no login link in e-mail: %q", f.mails[len(f.mails)-1])
	}
	return m
}

type env struct {
	t      *testing.T
	ts     *httptest.Server
	db     *db.DB
	mailer *fakeMailer
}

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
	t.Cleanup(func() { ts.Close(); d.Close() })
	return &env{t: t, ts: ts, db: d, mailer: mailer}
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
	link = e.ts.URL + link[strings.Index(link, "/auth/"):]
	c.get(link[len(e.ts.URL):], 303)
	if name != "" {
		c.post("/profile", 303, url.Values{"name": {name}})
	}
	return c
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
	want(t, admin.get("/admin/events/test-2026", 200), "Organisation")
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
	want(t, hx.post("/slots/3/unsignup", 200, nil), "gelé")
	settings.Set("freeze_at", "")
	admin.post("/test-2026/buvette/manage", 303, settings)

	// Pole manager: member management with live search.
	mgr := e.login("resp@example.test", "Rémi Responsable")
	mgr.get("/test-2026/buvette/manage", 200)
	mgr.get("/test-2026/organisation/manage", 403)
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

	// Volunteers cannot upload.
	vol := e.login("vol@example.test", "V")
	vol.upload("/ed/buvette/manage/image", "image", tinyPNG(t), 403)
	vol.upload("/admin/logo", "logo", tinyPNG(t), 403)
}
