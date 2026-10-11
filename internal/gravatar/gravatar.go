// Package gravatar fetches, for people who ask for it, the profile picture
// linked to their address on gravatar.com.
//
// The picture is fetched by the server and kept in the files store, so a
// browser only ever talks to this application: neither the IP address of a
// visitor nor the address hash of the person on screen reaches a third
// party. Nobody is looked up without asking: the first lookup is the
// person's own request, from their profile; after that the picture is kept
// up to date in the background, for the signed-in person only.
package gravatar

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"manage/internal/files"
	"manage/internal/store"
)

const (
	// TTL is how long a lookup is trusted, hit or miss: long enough that
	// browsing never costs a request, short enough that someone who sets up
	// their Gravatar sees it during the same edition.
	TTL = 30 * 24 * time.Hour

	// Endpoint is the public service.
	Endpoint = "https://gravatar.com"

	// size asked for, in pixels: twice the largest size shown, so the
	// picture stays sharp on a high-density screen.
	size = 256

	maxSize = 2 << 20
)

// Fetcher looks people up on gravatar.com and keeps what it finds.
type Fetcher struct {
	store   *store.Store
	files   *files.Store
	client  *http.Client
	baseURL string
	enabled bool

	// inflight keeps concurrent requests from fetching the same picture
	// twice: someone clicking through pages fires one lookup.
	inflight sync.Map // person id -> struct{}
	wg       sync.WaitGroup
}

// New builds a fetcher. When enabled is false every lookup is a no-op, for
// a deployment that must not reach gravatar.com. baseURL is Endpoint, or a
// test server.
func New(st *store.Store, fs *files.Store, enabled bool, baseURL string) *Fetcher {
	return &Fetcher{store: st, files: fs, enabled: enabled, baseURL: strings.TrimRight(baseURL, "/"),
		client: &http.Client{Timeout: 10 * time.Second}}
}

// Enabled tells whether lookups run.
func (f *Fetcher) Enabled() bool { return f.enabled }

// Hash is the identifier Gravatar expects: the SHA-256 of the lowercased,
// trimmed address.
func Hash(email string) string {
	sum := sha256.Sum256([]byte(strings.ToLower(strings.TrimSpace(email))))

	return hex.EncodeToString(sum[:])
}

// Refresh starts a lookup for p when their Gravatar is due for one, and
// returns at once: it runs during a request, which must never wait on a
// third party. Only people who chose their Gravatar are refreshed.
func (f *Fetcher) Refresh(p *store.Person) {
	if !f.enabled || !p.UsesGravatar() {
		return
	}

	if p.AvatarCheckedAt != nil && time.Since(*p.AvatarCheckedAt) < TTL {
		return
	}

	if _, busy := f.inflight.LoadOrStore(p.ID, struct{}{}); busy {
		return
	}

	f.wg.Add(1)

	go func() {
		defer f.wg.Done()
		defer f.inflight.Delete(p.ID)

		if _, err := f.Lookup(context.Background(), p.ID); err != nil {
			slog.Error("gravatar lookup", "person", p.UID, "err", err)
		}
	}()
}

// Lookup fetches the Gravatar of a person now and tells whether there is
// one. No Gravatar is not an error: the person keeps their initials. It
// runs on an explicit action of the person, or from Refresh.
func (f *Fetcher) Lookup(ctx context.Context, personID int64) (bool, error) {
	if !f.enabled {
		return false, nil
	}

	p, err := f.store.GetPerson(ctx, personID)
	if err != nil {
		return false, err
	}

	if !p.WantsGravatar() {
		return p.AvatarFile != "", nil
	}

	// Remember the attempt whatever its outcome, so that a service down, or
	// an address without Gravatar, is not asked again on every page.
	defer func() { _ = f.store.MarkGravatarChecked(context.Background(), personID) }()

	body, found, err := f.download(ctx, p.Email)
	if err != nil || !found {
		return false, err
	}

	name, err := f.files.Save(bytes.NewReader(body), "gravatar", files.KindLogo, maxSize)
	if err != nil {
		return false, fmt.Errorf("store gravatar: %w", err)
	}

	// The person may have uploaded a photo, or opted out, during the
	// download. Their choice wins.
	cur, err := f.store.GetPerson(ctx, personID)
	if err != nil || !cur.WantsGravatar() {
		_ = f.files.Remove(name)

		return false, err
	}

	old, err := f.store.SetPersonAvatar(ctx, personID, name, store.AvatarGravatar)
	if err != nil {
		_ = f.files.Remove(name)

		return false, err
	}

	if used, err := f.store.FileInUse(ctx, old); err == nil && !used {
		_ = f.files.Remove(old)
	}

	return true, nil
}

// download asks gravatar.com about an address. found is false when it
// answers 404, which d=404 makes it do for an unknown address.
func (f *Fetcher) download(ctx context.Context, email string) (body []byte, found bool, err error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	url := fmt.Sprintf("%s/avatar/%s?s=%d&d=404&r=g", f.baseURL, Hash(email), size)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, false, err
	}

	res, err := f.client.Do(req)
	if err != nil {
		return nil, false, err
	}
	defer res.Body.Close()

	switch res.StatusCode {
	case http.StatusOK:
	case http.StatusNotFound:
		return nil, false, nil
	default:
		return nil, false, fmt.Errorf("gravatar answered %s", res.Status)
	}

	body, err = io.ReadAll(io.LimitReader(res.Body, maxSize+1))
	if err != nil {
		return nil, false, err
	}

	return body, true, nil
}

// Wait blocks until every background lookup has finished, for the tests
// and a graceful shutdown.
func (f *Fetcher) Wait() { f.wg.Wait() }
