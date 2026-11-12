package server

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"net/url"
	"strings"
	"testing"
	"time"

	"manage/internal/files"
	"manage/internal/mail"
	"manage/internal/store"
)

// lastTo is the last message sent to addr.
func (c *captureMailer) lastTo(t *testing.T, addr string) mail.Message {
	t.Helper()
	c.mu.Lock()
	defer c.mu.Unlock()

	for i := len(c.msgs) - 1; i >= 0; i-- {
		for _, to := range c.msgs[i].To {
			if to == addr {
				return c.msgs[i]
			}
		}
	}

	t.Fatalf("no message to %s", addr)

	return mail.Message{}
}

func TestRoleReferents(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	f := e.volFixture()

	lead := e.loginAs(f.leadEmail)
	e.volunteer("nina@example.org", "Nina Ref", "/volunteers/")
	ninaP, _ := e.st.PersonByEmail(ctx, "nina@example.org")
	paul := e.volunteer("paul@example.org", "Paul", "/volunteers/")
	paulP, _ := e.st.PersonByEmail(ctx, "paul@example.org")
	mustOK(t, e.st.JoinPole(ctx, f.buvette.ID, paulP.ID))

	bar, err := e.st.CreateLocation(ctx, f.buvette.ID, "Bar")
	mustOK(t, err)
	_, err = e.st.CreateLocation(ctx, f.buvette.ID, "Cuisine")
	mustOK(t, err)

	// On the half hour, clear of the fixture's slots.
	start := time.Now().Add(96 * time.Hour).Truncate(time.Hour).Add(30 * time.Minute)
	sl := &store.Slot{PoleID: f.buvette.ID, LocationID: bar.ID, StartsAt: start, EndsAt: start.Add(time.Hour), Capacity: 2}
	mustOK(t, e.st.CreateSlot(ctx, sl))

	page := f.buvette.URL()
	slotPage := page + "/slots/" + sl.UID
	referents := f.buvette.ManageURL() + "/locations/" + bar.UID + "/referents"

	noReferents := func(why string) {
		t.Helper()

		if refs, _ := e.st.RoleReferents(ctx, f.buvette.ID); len(refs) != 0 {
			t.Fatalf("%s: %+v", why, refs)
		}
	}

	// Only a member of the pole can be one.
	_, body := e.post(lead, referents, url.Values{"person": {ninaP.UID}})
	mustContain(t, body, "doit être membre du pôle")
	noReferents("a non-member made a referent")

	// And only whoever runs the pole names one.
	e.post(paul, referents, url.Values{"person": {paulP.UID}})
	noReferents("a volunteer named a referent")

	mustOK(t, e.st.JoinPole(ctx, f.buvette.ID, ninaP.ID))

	_, body = e.post(lead, referents, url.Values{"person": {ninaP.UID}})
	mustContain(t, body, "Nina Ref")

	// On the slot's page, the role's referent comes first among the
	// contacts, then the leads.
	_, body = e.get(paul, slotPage)
	mustContain(t, body, "référent·e du poste", "responsable du pôle")

	if strings.Index(body, "Nina Ref") > strings.Index(body, "responsable du pôle") {
		t.Error("the referent does not come first")
	}

	// By role, the group names its referent.
	_, body = e.get(paul, page+"?layout=role")
	mustContain(t, body, "Référent·e :", `<strong class="font-medium text-gray-800">Nina Ref</strong>`)

	// The eve's reminder names the referent first, with why, and no way to
	// reach them; the HTML shows their picture, sent with the message,
	// small and square.
	pic := image.NewRGBA(image.Rect(0, 0, 300, 200))
	draw.Draw(pic, pic.Bounds(), image.NewUniform(color.RGBA{200, 80, 20, 255}), image.Point{}, draw.Src)

	var buf bytes.Buffer
	mustOK(t, png.Encode(&buf, pic))

	file, err := e.app.files.Save(&buf, "nina.png", files.KindLogo, 1<<20)
	mustOK(t, err)
	_, err = e.st.SetPersonAvatar(ctx, ninaP.ID, file, store.AvatarUpload)
	mustOK(t, err)

	_, err = e.st.Signup(ctx, sl.ID, paulP.ID)
	mustOK(t, err)

	n, err := e.app.sendVolunteerReminders(ctx, sl.StartsAt)
	if err != nil || n != 1 {
		t.Fatalf("reminders: %d %v", n, err)
	}

	msg := e.mailer.lastTo(t, "paul@example.org")
	mustContain(t, msg.Body, "Nina Ref (référent·e du poste) ; Lou Lead (responsable du pôle), lead@conf.test")
	mustContain(t, msg.HTML, `<img src="cid:avatar-`+ninaP.UID+`@manage"`)

	if strings.Contains(msg.Body+msg.HTML, "nina@example.org") {
		t.Error("the reminder gives the referent's address")
	}

	if len(msg.Inline) != 1 || msg.Inline[0].ID != "avatar-"+ninaP.UID+"@manage" {
		t.Fatalf("inline images: %+v", msg.Inline)
	}

	thumb, err := png.Decode(bytes.NewReader(msg.Inline[0].Data))
	if err != nil || thumb.Bounds().Dx() != avatarPx || thumb.Bounds().Dy() != avatarPx {
		t.Errorf("the picture sent: %v %v", thumb.Bounds(), err)
	}

	// Without a picture, their initials.
	if html, img := e.app.mailAvatar(&store.Person{Name: "Nina Ref"}); img != nil || !strings.Contains(html, ">NR</span>") {
		t.Errorf("no picture: %s %+v", html, img)
	}

	// On the day, the home page's slot of the moment names them too.
	now := time.Now()
	current := &store.Slot{PoleID: f.buvette.ID, LocationID: bar.ID, StartsAt: now.Add(-10 * time.Minute), EndsAt: now.Add(50 * time.Minute), Capacity: 2}
	mustOK(t, e.st.CreateSlot(ctx, current))
	mustOK(t, e.st.ForceSignup(ctx, current.ID, paulP.ID))

	_, body = e.get(paul, "/"+f.event.Slug+"/")
	mustContain(t, body, "En ce moment", "Nina Ref", "référent·e du poste", "responsable du pôle")

	// The planning names them on the cards of their role, and on paper; a
	// card swapped in there keeps them.
	planning := planningURL(f.event) + "?" + planURLQuery("all", f.buvette, "all", "")

	_, body = e.get(paul, planning)
	mustContain(t, body, `<span class="font-medium text-gray-800">Nina Ref</span>`, "Référent·e : Nina Ref")

	_, body = e.hx(paul, slotPage+"/unsignup", url.Values{"back": {planning}})
	mustContain(t, body, `<span class="font-medium text-gray-800">Nina Ref</span>`)

	// Not on the cards of another role, nor of the whole pole.
	_, body = e.get(paul, planningURL(f.event)+"?"+planURLQuery("all", f.buvette, dayKey(f.morning.StartsAt), ""))
	if strings.Contains(body, "Nina Ref") {
		t.Error("a slot of no role names a referent")
	}

	// Who leaves the pole stops being one.
	mustOK(t, e.st.LeavePole(ctx, f.buvette.ID, ninaP.ID))

	_, body = e.get(paul, slotPage)
	if strings.Contains(body, "référent·e du poste") {
		t.Error("a former member is still a referent")
	}

	// Taken off by the lead.
	mustOK(t, e.st.JoinPole(ctx, f.buvette.ID, ninaP.ID))
	e.post(lead, referents+"/"+ninaP.UID+"/delete", url.Values{})
	noReferents("the referent is still there")
}
