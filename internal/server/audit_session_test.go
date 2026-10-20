package server

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"manage/internal/config"
)

// TestStaffTokensFollowTheStaff: a staff token stops working once its owner
// has not logged in as staff for a while, and is revoked when they are
// refused as staff.
func TestStaffTokensFollowTheStaff(t *testing.T) {
	e := newEnv(t, func(c *config.Config) { c.AdminEmails = []string{"staff@conf.test"} })
	ctx := context.Background()

	e.adminClient()

	admin, err := e.st.UpsertAdmin(ctx, "staff@conf.test", "")
	mustOK(t, err)

	token, _, err := e.st.CreateAPIToken(ctx, admin.PersonID, &admin.ID, "Claude", false, time.Hour)
	mustOK(t, err)

	works := func() bool {
		resp, err := (&http.Client{Transport: bearer{token}}).Post(e.srv.URL+"/mcp", "application/json", strings.NewReader(`{}`))
		mustOK(t, err)
		resp.Body.Close()

		return resp.StatusCode != http.StatusUnauthorized
	}

	if !works() {
		t.Fatal("a fresh staff token refused")
	}

	// No staff login for over a month: the identity provider may have
	// taken them off the group since.
	defer func(d time.Duration) { staffTokenMaxIdle = d }(staffTokenMaxIdle)

	staffTokenMaxIdle = time.Nanosecond

	if works() {
		t.Error("a staff token works past the time without a staff login")
	}

	// Logging in again in time brings it back; being refused revokes it.
	staffTokenMaxIdle = time.Hour
	e.adminClient()

	if !works() {
		t.Fatal("the staff token after a new login")
	}

	e.app.cfg.AdminEmails = []string{"other@conf.test"}

	if code, _ := e.post(e.client(), "/admin/login", url.Values{"email": {"staff@conf.test"}}); code != http.StatusForbidden {
		t.Errorf("a refused staff login: %d", code)
	}

	e.app.cfg.AdminEmails = []string{"staff@conf.test"}

	if works() {
		t.Error("a staff token survives its owner being refused as staff")
	}
}

// TestStaffSessionEndsOffTheList: taken off the staff addresses, an open
// staff session ends at once.
func TestStaffSessionEndsOffTheList(t *testing.T) {
	e := newEnv(t, func(c *config.Config) { c.AdminEmails = []string{"staff@conf.test"} })
	admin := e.adminClient()

	if _, body := e.get(admin, "/admin/editions"); !strings.Contains(body, "Déconnexion") {
		t.Fatal("not logged in as staff")
	}

	e.app.cfg.AdminEmails = []string{"other@conf.test"}

	resp, err := noRedirect(admin).Get(e.srv.URL + "/admin/editions")
	mustOK(t, err)
	resp.Body.Close()

	if loc := resp.Header.Get("Location"); loc != "/admin/login" {
		t.Errorf("a former staff member's session: %d %s", resp.StatusCode, loc)
	}
}

// TestLogoutEndsEverySession: "log out" leaves nobody logged in, the staff
// included.
func TestLogoutEndsEverySession(t *testing.T) {
	e := newEnv(t)
	admin := e.adminClient()

	e.post(admin, "/logout", nil)

	resp, err := noRedirect(admin).Get(e.srv.URL + "/admin/editions")
	mustOK(t, err)
	resp.Body.Close()

	if loc := resp.Header.Get("Location"); loc != "/admin/login" {
		t.Errorf("still staff after logging out: %d %s", resp.StatusCode, loc)
	}
}

func TestEmailVerified(t *testing.T) {
	yes, no := true, false

	for _, c := range []struct {
		claim    *bool
		optOut   bool
		verified bool
	}{
		{&yes, false, true},
		{&no, false, false},
		{&no, true, false},
		{nil, false, false},
		{nil, true, true},
	} {
		s := &Server{cfg: &config.Config{OIDCUnverifiedEmail: c.optOut}}
		if got := s.emailVerified(c.claim); got != c.verified {
			t.Errorf("claim %v, opt-out %v: %v", c.claim, c.optOut, got)
		}
	}
}
