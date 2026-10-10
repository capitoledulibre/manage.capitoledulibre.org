// Package avatar fills in the profile picture of accounts that have not
// uploaded one, using gravatar.com.
//
// The picture is fetched by the server and cached in the upload directory, so
// that a volunteer's browser only ever talks to this application: neither
// their IP address nor the e-mail hash of the person on screen reaches a third
// party. Pages that merely display a volunteer therefore never trigger a
// lookup either; the fallback runs in the background for the signed-in
// account only, and the picture shows up on the next page load.
package avatar

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"benevoles/internal/db"
	"benevoles/internal/upload"
)

const (
	// TTL is how long a lookup is trusted, hit or miss: long enough that
	// browsing the planning never costs a request, short enough that a
	// volunteer who sets up their Gravatar sees it during the same edition.
	TTL = 30 * 24 * time.Hour

	// endpoint is the public Gravatar service. Tests point BaseURL elsewhere.
	endpoint = "https://gravatar.com"

	// size requested from gravatar.com, in pixels: twice the largest size we
	// display, so the picture stays sharp on a high-density screen.
	size = 256
)

// Fetcher looks accounts up on gravatar.com and caches what it finds.
type Fetcher struct {
	DB      *db.DB
	Uploads *upload.Store
	Client  *http.Client
	BaseURL string
	Enabled bool

	// inflight keeps concurrent requests from downloading the same picture
	// several times: a volunteer clicking through pages fires one lookup.
	inflight sync.Map // user id -> struct{}
	wg       sync.WaitGroup
}

// New builds a fetcher. When enabled is false every lookup is a no-op, which
// is what a deployment that must not reach out to gravatar.com wants.
func New(d *db.DB, store *upload.Store, enabled bool) *Fetcher {
	return &Fetcher{
		DB: d, Uploads: store, Enabled: enabled, BaseURL: endpoint,
		Client: &http.Client{Timeout: 10 * time.Second},
	}
}

// Hash is the account identifier Gravatar expects: the SHA-256 of the
// lowercased, trimmed e-mail address.
func Hash(email string) string {
	sum := sha256.Sum256([]byte(strings.ToLower(strings.TrimSpace(email))))
	return hex.EncodeToString(sum[:])
}

// Refresh starts a lookup for u when it needs one, and returns immediately:
// it is called from request handling and must never make the page wait on a
// third party.
func (f *Fetcher) Refresh(u db.User) {
	if !f.Enabled || !u.WantsGravatar() {
		return
	}
	if u.AvatarCheckedAt != nil && time.Since(*u.AvatarCheckedAt) < TTL {
		return
	}
	if _, busy := f.inflight.LoadOrStore(u.ID, struct{}{}); busy {
		return
	}
	f.wg.Add(1)
	go func() {
		defer f.wg.Done()
		defer f.inflight.Delete(u.ID)
		if _, err := f.Lookup(u.ID); err != nil {
			log.Printf("gravatar lookup for user %d: %v", u.ID, err)
		}
	}()
}

// Lookup fetches the Gravatar of one account now and reports whether one
// exists. A missing Gravatar is not an error: the account keeps its initials
// and is not looked up again before the TTL expires. Handlers on a request
// goroutine should prefer Refresh, except for an explicit user action.
func (f *Fetcher) Lookup(userID int64) (bool, error) {
	if !f.Enabled {
		return false, nil
	}
	u, err := f.DB.GetUser(userID)
	if err != nil {
		return false, err
	}
	if !u.WantsGravatar() {
		return u.Avatar != "", nil
	}
	// Remember the attempt whatever its outcome, so that a service that is
	// down - or an address with no Gravatar - is not retried on every view.
	defer f.DB.MarkGravatarChecked(userID)

	body, found, err := f.download(u.Email)
	if err != nil || !found {
		return false, err
	}
	name, err := f.Uploads.SaveBytes(body)
	if err != nil {
		return false, fmt.Errorf("store gravatar: %w", err)
	}
	// The volunteer may have uploaded a photo, or opted out, while the
	// download was in flight. Their choice wins.
	cur, err := f.DB.GetUser(userID)
	if err != nil || !cur.WantsGravatar() {
		f.Uploads.Remove(name)
		return false, err
	}
	old, err := f.DB.SetAvatar(userID, name, db.AvatarGravatar)
	if err != nil {
		f.Uploads.Remove(name)
		return false, err
	}
	if !f.DB.ImageInUse(old) {
		f.Uploads.Remove(old)
	}
	return true, nil
}

// download asks gravatar.com for one address. found is false when the service
// answers 404, which is what d=404 makes it do for an unknown address.
func (f *Fetcher) download(email string) (body []byte, found bool, err error) {
	url := fmt.Sprintf("%s/avatar/%s?s=%d&d=404&r=g", strings.TrimRight(f.BaseURL, "/"), Hash(email), size)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, false, err
	}
	res, err := f.Client.Do(req)
	if err != nil {
		return nil, false, err
	}
	defer res.Body.Close()
	if res.StatusCode == http.StatusNotFound {
		return nil, false, nil
	}
	if res.StatusCode != http.StatusOK {
		return nil, false, fmt.Errorf("gravatar answered %s", res.Status)
	}
	body, err = io.ReadAll(io.LimitReader(res.Body, upload.MaxSize+1))
	if err != nil {
		return nil, false, err
	}
	return body, true, nil
}

// Wait blocks until every background lookup has finished. Used by the tests
// and by the graceful shutdown.
func (f *Fetcher) Wait() { f.wg.Wait() }
