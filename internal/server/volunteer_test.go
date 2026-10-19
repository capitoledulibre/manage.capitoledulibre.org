package server

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"manage/internal/store"
)

// volFixture is an edition open to volunteers with a few poles.
type volFixture struct {
	event      *store.Event
	draft      *store.Event
	buvette    *store.Pole // public
	accueil    *store.Pole // private, with a key
	secret     *store.Pole // restricted
	morning    *store.Slot // Buvette, in three days, one place
	overlap    *store.Slot // Buvette, overlapping morning
	tomorrow   *store.Slot // Buvette, in a day
	lead       *store.Person
	leadEmail  string
	taskUID    string
	defaultURL string
}

func (e *env) volFixture() *volFixture {
	e.t.Helper()

	ctx := context.Background()
	f := &volFixture{leadEmail: "lead@conf.test"}

	f.event = &store.Event{Slug: "ec-2026", Name: "Example Conf 2026", Year: 2026}
	mustOK(e.t, e.st.SaveEvent(ctx, f.event))

	f.event.VolunteerStatus = store.VolunteersOpen
	mustOK(e.t, e.st.SaveVolunteerSettings(ctx, f.event))

	f.draft = &store.Event{Slug: "ec-2027", Name: "Example Conf 2027", Year: 2027}
	mustOK(e.t, e.st.SaveEvent(ctx, f.draft))

	f.buvette = &store.Pole{EventID: f.event.ID, Name: "Buvette"}
	f.accueil = &store.Pole{EventID: f.event.ID, Name: "Accueil", AccessMode: store.PolePrivate}
	f.secret = &store.Pole{EventID: f.event.ID, Name: "Trésorerie", AccessMode: store.PoleRestricted}

	for _, p := range []*store.Pole{f.buvette, f.accueil, f.secret} {
		mustOK(e.t, e.st.CreatePole(ctx, p))
	}

	start := time.Now().Add(72 * time.Hour).Truncate(time.Hour)
	f.morning = &store.Slot{PoleID: f.buvette.ID, StartsAt: start, EndsAt: start.Add(2 * time.Hour), Capacity: 1, Title: "Café"}
	f.overlap = &store.Slot{PoleID: f.buvette.ID, StartsAt: start.Add(time.Hour), EndsAt: start.Add(3 * time.Hour), Capacity: 3}
	f.tomorrow = &store.Slot{PoleID: f.buvette.ID, StartsAt: start.Add(-48 * time.Hour), EndsAt: start.Add(-46 * time.Hour), Capacity: 3}

	for _, sl := range []*store.Slot{f.morning, f.overlap, f.tomorrow} {
		mustOK(e.t, e.st.CreateSlot(ctx, sl))
	}

	task, err := e.st.CreateTask(ctx, f.morning.ID, "Allumer la machine")
	mustOK(e.t, err)
	f.taskUID = task.UID

	f.lead, err = e.st.EnsureVolunteer(ctx, f.leadEmail, "Lou Lead", "0611223344", "fr")
	mustOK(e.t, err)
	mustOK(e.t, e.st.AddPoleManager(ctx, f.buvette.ID, f.lead.ID))

	def, err := e.st.DefaultPole(ctx, f.event.ID)
	mustOK(e.t, err)
	f.defaultURL = def.URL()

	// Reload for the slugs and URLs.
	for _, p := range []**store.Pole{&f.buvette, &f.accueil, &f.secret} {
		*p, err = e.st.GetPole(ctx, (*p).ID)
		mustOK(e.t, err)
	}

	for _, sl := range []**store.Slot{&f.morning, &f.overlap, &f.tomorrow} {
		*sl, err = e.st.GetSlot(ctx, (*sl).ID, 0)
		mustOK(e.t, err)
	}

	return f
}

// hx posts a form as htmx does.
func (e *env) hx(c *http.Client, path string, form url.Values) (int, string) {
	e.t.Helper()

	req, _ := http.NewRequest(http.MethodPost, e.srv.URL+path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("HX-Request", "true")

	resp, err := c.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	e.app.bg.Wait()

	return resp.StatusCode, string(body)
}

// mailTo returns the bodies of the messages sent to addr.
func (c *captureMailer) mailTo(addr string) []string {
	c.mu.Lock()
	defer c.mu.Unlock()

	var out []string

	for _, m := range c.msgs {
		if m.To[0] == addr {
			out = append(out, m.Subject+"\n"+m.Body)
		}
	}

	return out
}

// volunteer signs a newcomer up through the volunteer login, the e-mailed
// link and the welcome form, heading for next.
func (e *env) volunteer(email, name, next string) *http.Client {
	e.t.Helper()

	c := e.client()

	code, body := e.post(c, "/login", url.Values{"email": {email}, "next": {next}})
	if code != 200 || !strings.Contains(body, "Un lien de connexion vient d&#39;être envoyé") {
		e.t.Fatalf("login form: %d\n%s", code, body)
	}

	link := e.mailer.lastLink(e.t, email)

	code, body = e.get(c, link+"?next="+url.QueryEscape(next))
	if code != 200 || !strings.Contains(body, `name="next" value="`+next+`"`) {
		e.t.Fatalf("confirm page: %d\n%s", code, body)
	}

	code, body = e.post(c, link, url.Values{"next": {next}})
	if code != 200 || !strings.Contains(body, "Bienvenue") {
		e.t.Fatalf("a newcomer is asked their name: %d\n%s", code, body)
	}

	if code, _ = e.post(c, "/volunteers/profile", url.Values{"welcome": {"1"}, "name": {name}, "next": {next}}); code != 200 {
		e.t.Fatalf("welcome form: %d", code)
	}

	return c
}

func TestVolunteerSignupFlow(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	f := e.volFixture()

	// A visitor is sent to the volunteer login, which names the edition.
	anon := e.client()
	code, body := e.get(anon, "/volunteers/")
	if code != 200 || !strings.Contains(body, "Espace bénévole") || !strings.Contains(body, "Example Conf 2026") {
		t.Fatalf("volunteer login: %d\n%s", code, body)
	}

	// The link of a pole names the pole on the login page.
	_, body = e.get(anon, f.buvette.URL())
	mustContain(t, body, "Vous avez été invité·e à rejoindre le pôle", "Buvette", "Lou Lead")

	nina := e.volunteer("nina@example.org", "Nina Bénévole", f.buvette.URL())

	ninaP, err := e.st.PersonByEmail(ctx, "nina@example.org")
	mustOK(t, err)

	if ninaP.Name != "Nina Bénévole" {
		t.Fatalf("name: %q", ninaP.Name)
	}

	_, body = e.get(nina, f.buvette.URL())
	mustContain(t, body, "Buvette", "Café", "Je m&#39;inscris", "Vous n&#39;êtes pas encore dans ce pôle")

	// Signing up on a public pole joins it: htmx reloads the page, which
	// shows the outcome.
	req, _ := http.NewRequest(http.MethodPost, e.srv.URL+f.morning.URL()+"/signup", strings.NewReader(url.Values{"back": {f.buvette.URL()}}.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("HX-Request", "true")

	resp, err := nina.Do(req)
	mustOK(t, err)
	resp.Body.Close()

	if resp.StatusCode != 200 || resp.Header.Get("HX-Redirect") != f.buvette.URL() {
		t.Fatalf("htmx signup joining the pole: %d %q", resp.StatusCode, resp.Header.Get("HX-Redirect"))
	}

	_, body = e.get(nina, f.buvette.URL())
	mustContain(t, body, "Vous avez rejoint le pôle", "Inscrit·e")

	// Withdrawing asks first, and focus goes to the state, not to the
	// button undoing the sign-up.
	mustContain(t, body, `data-confirm="Vous désinscrire de ce créneau`, `tabindex="-1" data-autofocus>✓`)

	if strings.Contains(body, "Vous n&#39;êtes pas encore dans ce pôle") {
		t.Error("the page still says the volunteer is not in the pole")
	}

	// Once in the pole, htmx gets the card back.
	code, body = e.hx(nina, f.morning.URL()+"/unsignup", url.Values{"back": {f.buvette.URL()}})
	if code != 200 || !strings.Contains(body, `id="slot-`+f.morning.UID+`"`) || strings.Contains(body, "<html") {
		t.Fatalf("htmx unsignup: %d\n%s", code, body)
	}

	mustContain(t, body, "Désinscription enregistrée", `hx-swap-oob="true"`)
	e.hx(nina, f.morning.URL()+"/signup", url.Values{"back": {f.buvette.URL()}})

	if ok, _ := e.st.IsPoleMember(ctx, f.buvette.ID, ninaP.ID); !ok {
		t.Fatal("a sign-up on a public pole joins it")
	}

	// An overlapping slot asks for a confirmation first.
	_, body = e.hx(nina, f.overlap.URL()+"/signup", url.Values{"back": {f.buvette.URL()}})
	mustContain(t, body, "M&#39;inscrire quand même", "Café")

	if mine, _ := e.st.ListPersonSlots(ctx, f.event.ID, ninaP.ID); len(mine) != 1 {
		t.Fatalf("an overlap is not signed up before confirmation: %d", len(mine))
	}

	// Without htmx, the confirmation is a page of its own.
	_, body = e.post(nina, f.overlap.URL()+"/signup", url.Values{"back": {f.buvette.URL()}})
	mustContain(t, body, "Ce créneau en chevauche un autre")

	_, body = e.post(nina, f.overlap.URL()+"/signup", url.Values{"back": {f.buvette.URL()}, "force": {"1"}})
	mustContain(t, body, "Inscription enregistrée", "Buvette")

	// The slot is full for the next volunteer, who waits for a place.
	omar := e.volunteer("omar@example.org", "Omar", "/volunteers/")

	_, body = e.hx(omar, f.morning.URL()+"/signup", nil)
	mustContain(t, body, "Complet", "Me prévenir si une place se libère")

	_, body = e.hx(omar, f.morning.URL()+"/waitlist", nil)
	mustContain(t, body, "Ne plus attendre", "Vous serez prévenu·e par e-mail")

	// Nina withdraws: Omar is told.
	_, body = e.hx(nina, f.morning.URL()+"/unsignup", nil)
	mustContain(t, body, "Désinscription enregistrée", "Je m&#39;inscris")

	waitFor(t, func() bool {
		for _, m := range e.mailer.mailTo("omar@example.org") {
			if strings.Contains(m, "Une place s'est libérée") && strings.Contains(m, f.morning.UID) {
				return true
			}
		}

		return false
	})

	// A withdrawal less than two days ahead warns the pole lead.
	e.hx(nina, f.tomorrow.URL()+"/signup", nil)
	e.hx(nina, f.tomorrow.URL()+"/unsignup", nil)

	waitFor(t, func() bool {
		for _, m := range e.mailer.mailTo(f.leadEmail) {
			if strings.Contains(m, "Désistement de dernière minute") && strings.Contains(m, "Nina Bénévole") {
				return true
			}
		}

		return false
	})

	// Omar takes the place and ticks the task; Nina, no longer on it, may not.
	e.hx(omar, f.morning.URL()+"/signup", nil)

	toggle := "/volunteers/" + f.event.Slug + "/" + f.buvette.Slug + "/tasks/" + f.taskUID + "/toggle"

	_, body = e.hx(omar, toggle, nil)
	mustContain(t, body, "fait par Omar", "Tâches : 1 sur 1 faites.")

	if code, _ = e.hx(nina, toggle, nil); code != http.StatusForbidden {
		t.Fatalf("a volunteer not on the slot ticks a task: %d", code)
	}

	// The slot page, the planning, the open slots and the calendar feed.
	_, body = e.get(omar, f.morning.URL())
	mustContain(t, body, "Allumer la machine", "Lou Lead", "Une question, un empêchement")

	_, body = e.get(omar, "/volunteers/"+f.event.Slug+"/planning?view=mine&day=all")
	mustContain(t, body, `aria-current="page">Mes créneaux`, "Buvette", "Café", "/calendar/")

	_, body = e.get(nina, "/volunteers/"+f.event.Slug+"/planning?view=open&day=all")
	mustContain(t, body, `aria-current="page">À pourvoir`, "Buvette")

	omarP, _ := e.st.PersonByEmail(ctx, "omar@example.org")
	token, _ := e.st.CalendarToken(ctx, omarP.ID)

	_, body = e.get(e.client(), "/calendar/"+token+".ics")
	mustContain(t, body, "BEGIN:VCALENDAR", "SUMMARY:Buvette – Café", f.morning.URL())

	if code, _ = e.get(e.client(), "/calendar/nope.ics"); code != http.StatusNotFound {
		t.Fatalf("unknown calendar token: %d", code)
	}

	// The home page shows the next slots and the poles.
	_, body = e.get(omar, "/volunteers/"+f.event.Slug)
	mustContain(t, body, "Bonjour Omar", "Mes prochains créneaux", "Buvette", "Accueil", "sur invitation")
}

func TestVolunteerPoleAccess(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	f := e.volFixture()

	nina := e.volunteer("nina@example.org", "Nina", "/volunteers/")

	// A restricted pole stays hidden from the others.
	_, body := e.get(nina, "/volunteers/"+f.event.Slug)
	if strings.Contains(body, "Trésorerie") {
		t.Error("a restricted pole is listed to a non-member")
	}

	if code, _ := e.get(nina, f.secret.URL()); code != http.StatusNotFound {
		t.Errorf("restricted pole: %d", code)
	}

	// A private pole takes its key.
	_, body = e.post(nina, f.accueil.URL()+"/join", url.Values{"key": {"wrong"}})
	mustContain(t, body, "Ce lien d&#39;invitation n&#39;est pas valable pour le pôle « Accueil »")

	_, body = e.get(nina, f.accueil.URL()+"/join?key="+f.accueil.AccessKey)
	mustContain(t, body, "Vous êtes invité·e à rejoindre ce pôle")

	_, body = e.post(nina, f.accueil.URL()+"/join", url.Values{"key": {f.accueil.AccessKey}})
	mustContain(t, body, "Vous avez rejoint le pôle « Accueil »")

	// A draft edition is only for the people preparing it.
	if code, _ := e.get(nina, "/volunteers/"+f.draft.Slug); code != http.StatusNotFound {
		t.Errorf("draft edition: %d", code)
	}

	admin := e.adminClient()
	if code, body := e.get(admin, "/volunteers/"+f.draft.Slug); code != 200 || !strings.Contains(body, "Brouillon") {
		t.Errorf("the staff see a draft edition: %d", code)
	}

	// The staff see the restricted pole too, under their own person.
	if code, _ := e.get(admin, f.secret.URL()); code != 200 {
		t.Errorf("the staff see a restricted pole: %d", code)
	}

	// The default pole cannot be left.
	_, body = e.post(nina, f.defaultURL+"/leave", nil)
	mustContain(t, body, "ne peut pas être quitté")

	// Leaving a pole cancels its slots.
	ninaP, _ := e.st.PersonByEmail(ctx, "nina@example.org")
	e.post(nina, f.morning.URL()+"/signup", url.Values{"force": {"1"}})

	_, body = e.post(nina, f.buvette.URL()+"/leave", nil)
	mustContain(t, body, "Vous avez quitté le pôle « Buvette »")

	if mine, _ := e.st.ListPersonSlots(ctx, f.event.ID, ninaP.ID); len(mine) != 0 {
		t.Errorf("leaving keeps %d slots", len(mine))
	}

	// Frozen: visible, but sign-ups are closed.
	f.event.VolunteerStatus = store.VolunteersFrozen
	mustOK(t, e.st.SaveVolunteerSettings(ctx, f.event))

	_, body = e.hx(nina, f.overlap.URL()+"/signup", nil)
	mustContain(t, body, "Les inscriptions sont closes sur ce créneau.")
}

func TestVolunteerLoginRules(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()

	// No edition takes volunteers: an unknown address gets nothing, as
	// before.
	e.post(e.client(), "/login", url.Values{"email": {"stranger@example.org"}})

	if n := e.mailer.count("stranger@example.org"); n != 0 {
		t.Fatalf("unknown address got %d mails", n)
	}

	f := e.volFixture()

	// With an edition open, a newcomer gets a link; the person only exists
	// once it is used.
	e.post(e.client(), "/login", url.Values{"email": {"stranger@example.org"}})

	if n := e.mailer.count("stranger@example.org"); n != 1 {
		t.Fatalf("newcomer got %d mails", n)
	}

	if _, err := e.st.PersonByEmail(ctx, "stranger@example.org"); err != store.ErrNotFound {
		t.Fatalf("a person was created before the link was used: %v", err)
	}

	// An unsafe next is ignored.
	c := e.client()
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

	resp, err := c.PostForm(e.srv.URL+e.mailer.lastLink(t, "stranger@example.org"), url.Values{"next": {"//evil.example/"}})
	mustOK(t, err)
	resp.Body.Close()

	if loc := resp.Header.Get("Location"); loc != "/volunteers/welcome?next=%2Fvolunteers%2F" {
		t.Fatalf("redirect after login: %q", loc)
	}

	// A known person with a name goes straight to where they were heading.
	lead := e.client()
	e.post(lead, "/login", url.Values{"email": {f.leadEmail}, "next": {f.buvette.URL()}})

	link := e.mailer.lastLink(t, f.leadEmail)
	if _, body := e.post(lead, link, url.Values{"next": {f.buvette.URL()}}); !strings.Contains(body, "Café") {
		t.Fatal("a known volunteer lands on the pole")
	}

	// A plain volunteer lands on the volunteer space from the home page.
	_, body := e.get(lead, "/")
	mustContain(t, body, "Bonjour Lou", "Les pôles")
}

func TestVolunteerPagesInEnglish(t *testing.T) {
	e := newEnv(t)
	f := e.volFixture()

	nina := e.volunteer("nina@example.org", "Nina", "/volunteers/")
	e.get(nina, "/lang/en")

	_, body := e.get(nina, "/volunteers/"+f.event.Slug)
	mustContain(t, body, "Hello Nina", "Teams", "by invitation", "Schedule")

	_, body = e.post(nina, f.morning.URL()+"/signup", url.Values{"back": {f.buvette.URL()}})
	mustContain(t, body, "Signed up", "You joined the “Buvette” team.")
}

// TestNoNumericIDsInVolunteerPages crawls the volunteer pages for integer
// ids, as TestNoNumericIDsInPages does for the others.
func TestNoNumericIDsInVolunteerPages(t *testing.T) {
	e := newEnv(t)
	f := e.volFixture()

	nina := e.volunteer("nina@example.org", "Nina", "/volunteers/")
	e.post(nina, f.morning.URL()+"/signup", nil)
	e.post(nina, f.accueil.URL()+"/join", url.Values{"key": {f.accueil.AccessKey}})

	numeric := regexp.MustCompile(`(?:href|action|hx-post|hx-get|hx-target)="[^"]*[/#][0-9]+(?:[/"?#])` +
		`|type="(?:hidden|checkbox|radio)" name="[^"]*" value="[0-9]+"`)

	for _, path := range []string{
		"/volunteers/" + f.event.Slug, "/volunteers/" + f.event.Slug + "/planning", "/volunteers/" + f.event.Slug + "/planning?view=mine",
		f.buvette.URL(), f.accueil.URL(), f.morning.URL(), f.defaultURL, "/volunteers/profile",
	} {
		code, body := e.get(nina, path)
		if code != 200 {
			t.Errorf("%s: status %d", path, code)

			continue
		}

		for _, m := range numeric.FindAllString(body, -1) {
			t.Errorf("%s exposes an integer id: %s", path, m)
		}
	}
}
