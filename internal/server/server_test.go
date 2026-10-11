package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"image"
	"image/png"
	"io"
	"io/fs"
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

	"manage/internal/config"
	"manage/internal/files"
	"manage/internal/i18n"
	"manage/internal/mail"
	"manage/internal/store"
	"manage/web"
)

type captureMailer struct {
	mu   sync.Mutex
	msgs []mail.Message
	// fail makes Send return an error, to simulate an SMTP outage.
	fail bool
	// malformed lists the subjects of messages with a formatting error.
	malformed []string
}

func (c *captureMailer) Send(m mail.Message) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.fail {
		return errors.New("smtp down")
	}

	// A text formatted with the wrong number of arguments says so with
	// "%!": no e-mail may go out like that.
	if strings.Contains(m.Subject+m.Body, "%!") {
		c.malformed = append(c.malformed, m.Subject)
	}

	c.msgs = append(c.msgs, m)

	return nil
}

func (c *captureMailer) setFail(v bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.fail = v
}

var linkRe = regexp.MustCompile(`/auth/[A-Za-z0-9_-]+`)

// lastLink returns the magic link path of the last email sent to addr.
func (c *captureMailer) lastLink(t *testing.T, addr string) string {
	t.Helper()
	c.mu.Lock()
	defer c.mu.Unlock()

	for i := len(c.msgs) - 1; i >= 0; i-- {
		if c.msgs[i].To[0] == addr {
			if l := linkRe.FindString(c.msgs[i].Body); l != "" {
				return l
			}
		}
	}

	t.Fatalf("no magic link sent to %s", addr)

	return ""
}

func (c *captureMailer) count(addr string) int {
	c.mu.Lock()
	defer c.mu.Unlock()

	n := 0

	for _, m := range c.msgs {
		if m.To[0] == addr {
			n++
		}
	}

	return n
}

type env struct {
	t      *testing.T
	srv    *httptest.Server
	st     *store.Store
	mailer *captureMailer
	app    *Server
}

func newEnv(t *testing.T, opts ...func(*config.Config)) *env {
	t.Helper()

	dir := t.TempDir()
	ctx := context.Background()

	st, err := store.Open(ctx, filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { st.Close() })

	fstore, err := files.New(filepath.Join(dir, "files"))
	if err != nil {
		t.Fatal(err)
	}

	cfg := &config.Config{Dev: true, ExportToken: "secret", ReminderInterval: time.Hour, SMTPTLS: "none"}
	for _, o := range opts {
		o(cfg)
	}

	// The event name is package state in i18n: restore the default.
	t.Cleanup(func() { i18n.SetSite(config.DefaultSiteName) })
	m := &captureMailer{}

	t.Cleanup(func() {
		m.mu.Lock()
		defer m.mu.Unlock()

		for _, s := range m.malformed {
			t.Errorf("e-mail with a formatting error: %s", s)
		}
	})

	app, err := New(ctx, cfg, st, fstore, m)
	if err != nil {
		t.Fatal(err)
	}

	srv := httptest.NewServer(app.Handler())
	t.Cleanup(srv.Close)
	cfg.BaseURL = srv.URL

	return &env{t: t, srv: srv, st: st, mailer: m, app: app}
}

func (e *env) client() *http.Client {
	jar, _ := cookiejar.New(nil)

	return &http.Client{Jar: jar}
}

func (e *env) get(c *http.Client, path string) (int, string) {
	e.t.Helper()

	resp, err := c.Get(e.srv.URL + path)
	if err != nil {
		e.t.Fatal(err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	return resp.StatusCode, string(body)
}

func (e *env) post(c *http.Client, path string, form url.Values) (int, string) {
	e.t.Helper()

	resp, err := c.PostForm(e.srv.URL+path, form)
	if err != nil {
		e.t.Fatal(err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	// E-mails go out off the request: let them leave before the test looks.
	e.app.bg.Wait()

	return resp.StatusCode, string(body)
}

func (e *env) upload(c *http.Client, path string, fields map[string]string, field, filename string, data []byte) (int, string) {
	e.t.Helper()

	var buf bytes.Buffer

	mw := multipart.NewWriter(&buf)
	for k, v := range fields {
		_ = mw.WriteField(k, v)
	}

	fw, _ := mw.CreateFormFile(field, filename)
	_, _ = fw.Write(data)
	_ = mw.Close()

	resp, err := c.Post(e.srv.URL+path, mw.FormDataContentType(), &buf)
	if err != nil {
		e.t.Fatal(err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	return resp.StatusCode, string(body)
}

// loginManager follows the magic link flow for addr.
func (e *env) loginManager(c *http.Client, addr string) {
	e.t.Helper()

	link := e.mailer.lastLink(e.t, addr)

	if code, body := e.get(c, link); code != 200 || !strings.Contains(body, addr) {
		e.t.Fatalf("confirm page: %d", code)
	}

	if code, body := e.post(c, link, nil); code != 200 || !strings.Contains(body, "Tableau de bord") {
		e.t.Fatalf("consume link: %d\n%s", code, body)
	}
}

func pngBytes() []byte {
	var buf bytes.Buffer
	_ = png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 4, 4)))

	return buf.Bytes()
}

func mustContain(t *testing.T, body string, want ...string) {
	t.Helper()

	for _, w := range want {
		if !strings.Contains(body, w) {
			t.Errorf("response does not contain %q", w)
		}
	}
}

func TestFullFlow(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()

	admin := e.client()

	if code, _ := e.post(admin, "/admin/login", url.Values{"email": {"staff@conf.test"}}); code != 200 {
		t.Fatalf("admin login: %d", code)
	}

	code, body := e.post(admin, "/admin/events/new", url.Values{
		"name": {"Example Conf 2026"}, "year": {"2026"}, "is_current": {"on"}, "logo_deadline": {"2026-11-01"},
	})
	if code != 200 {
		t.Fatalf("create event: %d", code)
	}

	ev, err := e.st.GetEventBySlug(ctx, "example-conf-2026")
	if err != nil {
		t.Fatal(err)
	}

	e.post(admin, "/admin/events/"+ev.Slug+"/levels", url.Values{"name": {"Or"}, "position": {"2"}, "price": {"1000"}, "max_attendees": {"1"}})

	levels, _ := e.st.ListLevels(ctx, ev.ID)
	if len(levels) != 1 {
		t.Fatalf("levels: %d", len(levels))
	}

	e.post(admin, "/admin/events/"+ev.Slug+"/perks", url.Values{"name_fr": {"Logo sur le site"}, "name_en": {"Logo on the website"}})
	perks, _ := e.st.ListPerks(ctx, ev.ID)
	e.post(admin, "/admin/events/"+ev.Slug+"/matrix", url.Values{"lp": {levels[0].UID + ":" + perks[0].UID}})

	code, body = e.post(admin, "/admin/organizations", url.Values{
		"name": {"Acme Corp"}, "event": {ev.Slug}, "status": {"confirmed"},
		"manager_email": {"alice@acme.test"}, "send_invite": {"on"},
	})
	if code != 200 {
		t.Fatalf("create org: %d", code)
	}

	// Creating a partner opens its file on the new edition.
	mustContain(t, body, "Acme Corp", "Pipeline Example Conf 2026", `aria-current="page"`)

	parts, _ := e.st.ListEventPartnerships(ctx, ev.ID)
	p := parts[0]
	acme, _ := e.st.GetOrganization(ctx, p.OrgID)
	pURL := partnershipURL(acme, ev)
	e.post(admin, pURL, url.Values{"status": {"confirmed"}, "level": {levels[0].UID}})

	// Manager logs in with the invitation link.
	alice := e.client()
	e.loginManager(alice, "alice@acme.test")

	// A magic link is single use.
	if code, body := e.post(e.client(), e.mailer.lastLink(t, "alice@acme.test"), nil); code != 200 || !strings.Contains(body, "invalide") {
		t.Errorf("reused link should be rejected")
	}

	_, body = e.get(alice, "/partners/")
	mustContain(t, body, "Acme Corp", "Or", "Logo sur le site", "Devis reçu", "Envoyer votre logo")

	// Organization details.
	code, _ = e.post(alice, "/partners/organization", url.Values{
		"name": {"Acme"}, "legal_name": {"Acme SAS"}, "address_line1": {"1 rue du Libre"}, "zip_code": {"31000"},
		"city": {"Toulouse"}, "website": {"acme.test"}, "description_fr": {"Nous aimons le libre."},
	})
	if code != 200 {
		t.Fatalf("update org: %d", code)
	}

	org, _ := e.st.GetOrganization(ctx, p.OrgID)
	if org.Website != "https://acme.test" || !org.BillingComplete() {
		t.Errorf("org not updated: %+v", org)
	}

	// Logo upload: a text file is refused, a PNG accepted.
	_, body = e.upload(alice, "/partners/logo", map[string]string{"variant": "default"}, "logo", "logo.png", []byte("not an image"))
	mustContain(t, body, "Format non accepté")

	_, body = e.upload(alice, "/partners/logo", map[string]string{"variant": "default"}, "logo", "logo.png", pngBytes())
	mustContain(t, body, "Logo enregistré")

	org, _ = e.st.GetOrganization(ctx, p.OrgID)
	if org.LogoFile == "" {
		t.Fatal("logo not saved")
	}

	if code, _ := e.get(e.client(), "/logos/"+org.LogoFile); code != 200 {
		t.Errorf("public logo: %d", code)
	}

	// Attendees, with the level limit of one.
	e.post(alice, "/partners/attendees", url.Values{"first_name": {"Bob"}, "last_name": {"Martin"}, "role": {"booth"}})
	_, body = e.post(alice, "/partners/attendees", url.Values{"first_name": {"Carol"}, "last_name": {"Durand"}})
	mustContain(t, body, "Bob", "nombre de participants")

	if n, _ := e.st.CountAttendees(ctx, p.ID); n != 1 {
		t.Errorf("attendees: %d", n)
	}

	// Job offers appear on the public page.
	e.post(alice, "/partners/jobs/new", url.Values{"title": {"Développeuse Go"}, "url": {"https://acme.test/jobs/go"}, "contract": {"CDI"}})
	_, body = e.get(e.client(), "/jobs")
	mustContain(t, body, "Développeuse Go", "Acme")

	// Invite a colleague, who can then request a login link.
	e.post(alice, "/partners/managers", url.Values{"email": {"dave@acme.test"}, "lang": {"en"}})
	dave := e.client()
	e.post(dave, "/login", url.Values{"email": {"dave@acme.test"}})
	link := e.mailer.lastLink(t, "dave@acme.test")
	e.get(dave, link)

	if code, body := e.post(dave, link, nil); code != 200 || !strings.Contains(body, "Dashboard") {
		t.Errorf("dave should see the English dashboard (%d)", code)
	}

	// Unknown emails get the same answer and no email.
	_, body = e.post(e.client(), "/login", url.Values{"email": {"nobody@example.test"}})
	mustContain(t, body, "Vérifiez votre boîte mail")

	if e.mailer.count("nobody@example.test") != 0 {
		t.Error("email sent to unknown address")
	}

	// Staff uploads the quote: the partner can view it, others cannot.
	pdf := []byte("%PDF-1.4\n%fake\n")
	e.upload(admin, pURL+"/documents", map[string]string{"kind": "quote", "notify": "on"}, "file", "devis.pdf", pdf)

	if code, _ := e.get(alice, "/partners/documents/"+ev.Slug+"/quote"); code != 200 {
		t.Errorf("partner quote: %d", code)
	}

	e.post(admin, "/admin/organizations", url.Values{"name": {"Other Inc"}, "event": {ev.Slug}, "status": {"confirmed"}, "manager_email": {"eve@other.test"}, "send_invite": {"on"}})

	eve := e.client()
	e.loginManager(eve, "eve@other.test")

	if code, _ := e.get(eve, "/partners/documents/"+ev.Slug+"/quote"); code != 404 {
		t.Errorf("other org must not access the quote: %d", code)
	}

	if code, _ := e.get(e.client(), "/admin/files/"+org.LogoFile); code != 200 {
		// Anonymous clients are redirected to the login page (200 after redirect).
		t.Errorf("admin files redirect: %d", code)
	}

	// Anonymous users are sent to the login page.
	_, body = e.get(e.client(), "/partners/")
	mustContain(t, body, "Recevoir un lien")

	// Export.
	if code, _ := e.get(e.client(), "/api/partners.json"); code != 401 {
		t.Errorf("export without token: %d", code)
	}

	// The token goes in the header, not in the query string.
	if code, _ := e.get(e.client(), "/api/partners.json?token=secret"); code != 401 {
		t.Errorf("export with the token in the query: %d", code)
	}

	req, _ := http.NewRequest(http.MethodGet, e.srv.URL+"/api/partners.json", nil)
	req.Header.Set("Authorization", "Bearer secret")

	resp, err := e.client().Do(req)
	mustOK(t, err)

	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	body = string(raw)

	var doc struct {
		Levels []struct {
			Name     string
			Partners []struct {
				Name, Logo string
				Hiring     bool
			}
		}
	}

	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		t.Fatalf("export json: %v\n%s", err, body)
	}

	if len(doc.Levels) < 1 || doc.Levels[0].Name != "Or" || len(doc.Levels[0].Partners) != 1 ||
		doc.Levels[0].Partners[0].Logo == "" || !doc.Levels[0].Partners[0].Hiring {
		t.Errorf("unexpected export: %s", body)
	}

	// Pipeline renders.
	_, body = e.get(admin, "/admin/events/"+ev.Slug)
	mustContain(t, body, "Acme", "Other Inc", "1\u202f000")

	// Impersonation.
	e.post(admin, "/admin/organizations/"+org.Slug+"/view-as", nil)
	_, body = e.get(admin, "/partners/")
	mustContain(t, body, "en tant que staff", "Acme")
}

func TestReminders(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()

	deadline := time.Date(2026, 11, 10, 0, 0, 0, 0, time.UTC)
	ev := &store.Event{Slug: "ec", Name: "EC", Year: 2026, IsCurrent: true, LogoDeadline: &deadline}

	if err := e.st.SaveEvent(ctx, ev); err != nil {
		t.Fatal(err)
	}

	org := &store.Organization{Name: "Acme"}
	_ = e.st.CreateOrganization(ctx, org)
	_, _ = e.st.AddManager(ctx, &store.Manager{OrgID: org.ID, Email: "a@acme.test"})
	_ = e.st.CreatePartnership(ctx, &store.Partnership{OrgID: org.ID, EventID: ev.ID, Status: store.StatusConfirmed})

	run := func(day int) {
		if err := e.app.sendReminders(ctx, time.Date(2026, 11, day, 9, 0, 0, 0, time.UTC)); err != nil {
			t.Fatal(err)
		}
	}

	run(1) // 9 days before: 14-day reminder.
	run(2) // Already sent.
	run(8) // 2 days before: 3-day reminder.
	run(9)

	if n := e.mailer.count("a@acme.test"); n != 2 {
		t.Errorf("expected 2 reminders, got %d", n)
	}
}

// TestTemplateKeys checks that every literal translation key used in the
// templates exists in the catalog.
func TestTemplateKeys(t *testing.T) {
	re := regexp.MustCompile(`\{\{t [^}"]*"([a-z_.]+)"`)

	err := fs.WalkDir(web.Templates, "templates", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}

		data, err := fs.ReadFile(web.Templates, path)
		if err != nil {
			return err
		}

		for _, m := range re.FindAllStringSubmatch(string(data), -1) {
			if m[1] != "fr" && m[1] != "en" && !i18n.Has(m[1]) {
				t.Errorf("%s: missing translation %q", path, m[1])
			}
		}

		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	for _, k := range []string{"logo.variant_default", "logo.variant_dark", "doc.brochure", "doc.guide",
		"mail.reminder.logo", "mail.reminder.attendees", "mail.quote.subject", "mail.quote.body", "mail.invoice.subject", "mail.invoice.body"} {
		if !i18n.Has(k) {
			t.Errorf("missing translation %q", k)
		}
	}
}
