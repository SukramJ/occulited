// Package sysupdate is the release feed check for the system firmware (task 16): what
// checkFirmwareUpdate.sh did for OpenCCU - ask the releases API once a day, pick the asset for
// this product, compare versions - plus a download that stages the asset through the same path
// an upload takes, checked against the published sha256.
package sysupdate

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"path"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/hobbyquaker/occulited/internal/system"
)

// feedErrorText is what an error answer of the feed says, for the Status page (B-115): GitHub's
// JSON carries a "message" beside a documentation URL, and the raw body was one run of text wider
// than a phone ("API rate limit exceeded for …"). Another body stays as it came, trimmed and cut at
// feedErrorMax characters.
func feedErrorText(body []byte) string {
	var j struct {
		Message string `json:"message"`
	}
	if json.Unmarshal(body, &j) == nil && strings.TrimSpace(j.Message) != "" {
		body = []byte(j.Message)
	}
	s := strings.TrimSpace(string(body))
	if r := []rune(s); len(r) > feedErrorMax {
		s = string(r[:feedErrorMax]) + "…"
	}
	return s
}

const feedErrorMax = 300

// Available is the newest release the feed offers for this product.
type Available struct {
	Version   string `json:"version"`
	Tag       string `json:"tag"`
	Name      string `json:"name"`
	URL       string `json:"url"`
	Size      int64  `json:"size"`
	SHA256URL string `json:"sha256_url,omitempty"`
	Published string `json:"published,omitempty"`
	Notes     string `json:"notes_url,omitempty"`
	// Newer: on openccu-lite a semantically newer version; on OpenCCU one that differs (a downgrade
	// counts, like the script).
	Newer bool `json:"newer"`
}

// State is what the API reports.
type State struct {
	Enabled   bool       `json:"enabled"`
	FeedURL   string     `json:"feed_url"`
	Checked   string     `json:"checked,omitempty"`
	Error     string     `json:"error,omitempty"`
	Available *Available `json:"available"`
	// Downloading is set while a release is being fetched and staged.
	Downloading string `json:"downloading,omitempty"`
}

// Service checks the feed and downloads releases.
type Service struct {
	Root    system.Root
	FeedURL string // a GitHub "releases/latest" URL (or any JSON of that shape)
	HTTP    *http.Client
	Log     *slog.Logger
	Enabled bool

	mu          sync.Mutex
	etag        string
	checked     time.Time
	err         string
	available   *Available
	downloading string
}

// New returns a service for the feed; enabled decides whether the daily check runs.
func New(root system.Root, feedURL string, enabled bool, log *slog.Logger) *Service {
	return &Service{Root: root, FeedURL: feedURL, HTTP: &http.Client{Timeout: 30 * time.Minute}, Log: log, Enabled: enabled}
}

// State reports the last check.
func (s *Service) State() State {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := State{Enabled: s.Enabled, FeedURL: s.FeedURL, Error: s.err, Available: s.available, Downloading: s.downloading}
	if !s.checked.IsZero() {
		st.Checked = s.checked.Format(time.RFC3339)
	}
	return st
}

// liteProducts maps the upstream PRODUCT that D-31 keeps in /VERSION to the openccu-lite product
// name the release files carry (D-39, D-43). It is the inverse of the case in the fork's
// board/lite/post-build.sh and its twin in the recovery system's, and the three must be changed
// together: a box whose PRODUCT is missing here cannot find its own image in the feed.
var liteProducts = map[string]string{
	"ova":  "x86_64-ova",
	"rpi3": "aarch64-rpi3",
	"rpi4": "aarch64-rpi4",
	"rpi5": "aarch64-rpi5",
	// task 34: the Proxmox CT templates, named after the board (lxc-amd64), not the product
	// (lxc-lite_amd64), as board/lxc-lite/post-release.sh writes them; a .tar.xz, and the check
	// only ever *names* the newer template - it is swapped on the host, never staged from inside
	"lxc_amd64": "lxc-amd64",
	"lxc_arm64": "lxc-arm64",
}

// assetPattern is what the feed must carry for this box. On openccu-lite (D-44) that is
// openccu-lite-<product>-<version>.zip with the lite product looked up from the upstream PRODUCT
// that D-31 keeps in /VERSION; on OpenCCU it is OpenCCU-<version>-<PRODUCT>.<ext>, the extension
// following the platform as in checkFirmwareUpdate.sh.
func assetPattern(v system.Version) (prefix, suffix string, err error) {
	if v.Product == "" {
		return "", "", errors.New("/VERSION has no PRODUCT")
	}
	ext := "zip"
	switch v.Platform {
	case "oci":
		return "", "", errors.New("a container is updated by pulling the new image, not from inside")
	case "lxc":
		ext = "tar.xz"
	}
	if v.Variant == "lite" {
		name, ok := liteProducts[v.Product]
		if !ok {
			return "", "", fmt.Errorf("no openccu-lite image for PRODUCT=%s", v.Product)
		}
		return "openccu-lite-" + name + "-", "." + ext, nil
	}
	return "OpenCCU-", "-" + v.Product + "." + ext, nil
}

// semverNewer says a is a newer version than b, both semantic versions with an optional
// prerelease (1.0.0-alpha.0 < 1.0.0-alpha.1 < 1.0.0-beta.0 < 1.0.0-rc.1 < 1.0.0 < 1.0.1).
// Anything that is not a semantic version falls back to "differs", which is what the old
// script did.
func semverNewer(a, b string) bool {
	pa, oka := parseSemver(a)
	pb, okb := parseSemver(b)
	if !oka || !okb {
		return a != b
	}
	for i := 0; i < 3; i++ {
		if pa.num[i] != pb.num[i] {
			return pa.num[i] > pb.num[i]
		}
	}
	// a release outranks any prerelease of the same numbers
	if pa.pre == "" || pb.pre == "" {
		return pa.pre == "" && pb.pre != ""
	}
	return comparePre(pa.pre, pb.pre) > 0
}

type semver struct {
	num [3]int
	pre string
}

var semverRe = regexp.MustCompile(`^(\d+)\.(\d+)\.(\d+)(?:-([0-9A-Za-z.-]+))?(?:\+[0-9A-Za-z.-]+)?$`)

func parseSemver(s string) (semver, bool) {
	m := semverRe.FindStringSubmatch(s)
	if m == nil {
		return semver{}, false
	}
	var v semver
	for i := 0; i < 3; i++ {
		v.num[i], _ = strconv.Atoi(m[i+1])
	}
	v.pre = m[4]
	return v, true
}

// comparePre orders prerelease strings by their dot-separated identifiers: numeric ones
// numerically, others lexically, numeric before alphanumeric, a shorter list first.
func comparePre(a, b string) int {
	as, bs := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < len(as) && i < len(bs); i++ {
		na, ea := strconv.Atoi(as[i])
		nb, eb := strconv.Atoi(bs[i])
		switch {
		case ea == nil && eb == nil:
			if na != nb {
				if na > nb {
					return 1
				}
				return -1
			}
		case ea == nil:
			return -1
		case eb == nil:
			return 1
		default:
			if c := strings.Compare(as[i], bs[i]); c != 0 {
				return c
			}
		}
	}
	return len(as) - len(bs)
}

// Check fetches the feed once (ETag-cached) and remembers the result.
func (s *Service) Check(ctx context.Context) error {
	v := s.Root.ReadVersion()
	prefix, suffix, err := assetPattern(v)
	if err != nil {
		s.remember(nil, err)
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.FeedURL, nil)
	if err != nil {
		s.remember(nil, err)
		return err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	s.mu.Lock()
	if s.etag != "" && s.available != nil {
		req.Header.Set("If-None-Match", s.etag)
	}
	s.mu.Unlock()
	res, err := s.HTTP.Do(req)
	if err != nil {
		s.remember(nil, err)
		return err
	}
	defer res.Body.Close()
	if res.StatusCode == http.StatusNotModified {
		s.mu.Lock()
		s.checked, s.err = time.Now(), ""
		s.mu.Unlock()
		return nil
	}
	if res.StatusCode == http.StatusNotFound {
		// GitHub answers 404 on releases/latest when a repository has no published release, which
		// is every box before release day (D-24) - and "HTTP 404 Not Found" on the Status page
		// reads like a broken box rather than "there is nothing yet".
		err := errors.New("no release published yet on the update feed")
		s.remember(nil, err)
		return err
	}
	if res.StatusCode != 200 {
		msg, _ := io.ReadAll(io.LimitReader(res.Body, 4096))
		err := fmt.Errorf("feed: HTTP %d %s", res.StatusCode, feedErrorText(msg))
		s.remember(nil, err)
		return err
	}
	var rel struct {
		Tag         string `json:"tag_name"`
		Name        string `json:"name"`
		HTMLURL     string `json:"html_url"`
		PublishedAt string `json:"published_at"`
		Assets      []struct {
			Name string `json:"name"`
			URL  string `json:"browser_download_url"`
			Size int64  `json:"size"`
		} `json:"assets"`
	}
	if err := json.NewDecoder(io.LimitReader(res.Body, 4<<20)).Decode(&rel); err != nil {
		s.remember(nil, fmt.Errorf("feed: %w", err))
		return err
	}
	var av *Available
	for _, a := range rel.Assets {
		if !strings.HasSuffix(a.Name, suffix) || !strings.HasPrefix(a.Name, prefix) || len(a.Name) <= len(prefix)+len(suffix) {
			continue
		}
		version := strings.TrimSuffix(strings.TrimPrefix(a.Name, prefix), suffix)
		newer := version != v.Full()
		if v.Variant == "lite" {
			newer = semverNewer(version, v.Full())
		}
		av = &Available{Version: version, Tag: rel.Tag, Name: a.Name, URL: a.URL, Size: a.Size, Published: rel.PublishedAt, Notes: rel.HTMLURL, Newer: newer}
		for _, b := range rel.Assets {
			if b.Name == a.Name+".sha256" {
				av.SHA256URL = b.URL
			}
		}
		break
	}
	if av == nil {
		err := fmt.Errorf("feed: release %s carries no %s*%s", rel.Tag, prefix, suffix)
		s.remember(nil, err)
		return err
	}
	s.mu.Lock()
	s.etag = res.Header.Get("ETag")
	s.mu.Unlock()
	s.remember(av, nil)
	return nil
}

func (s *Service) remember(av *Available, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.checked = time.Now()
	if err != nil {
		s.err = err.Error()
		return
	}
	s.err, s.available = "", av
}

// SetEnabled switches the daily check (task 244: the page's *Check daily*); the button's check
// runs either way.
func (s *Service) SetEnabled(on bool) {
	s.mu.Lock()
	s.Enabled = on
	s.mu.Unlock()
}

func (s *Service) enabled() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.Enabled
}

// Run checks shortly after start and then once a day with jitter - each time only when the daily
// check is on then (task 244: it can be switched while the system runs).
func (s *Service) Run(ctx context.Context) {
	first := time.After(2*time.Minute + time.Duration(rand.Int64N(int64(5*time.Minute))))
	for {
		select {
		case <-ctx.Done():
			return
		case <-first:
			if s.enabled() {
				_ = s.Check(ctx)
			}
		case <-time.After(24*time.Hour + time.Duration(rand.Int64N(int64(2*time.Hour)))):
			if s.enabled() {
				_ = s.Check(ctx)
			}
		}
	}
}

// Download fetches the available release, verifies its sha256 against the published file when
// there is one, and stages it as an upload would be. One download at a time.
func (s *Service) Download(ctx context.Context) (*system.StagedUpdate, error) {
	s.mu.Lock()
	av := s.available
	if s.downloading != "" {
		s.mu.Unlock()
		return nil, errors.New("a download is running")
	}
	if av == nil {
		s.mu.Unlock()
		return nil, errors.New("no release known - check first")
	}
	s.downloading = av.Name
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.downloading = ""
		s.mu.Unlock()
	}()
	want := ""
	if av.SHA256URL != "" {
		res, err := s.HTTP.Get(av.SHA256URL)
		if err == nil {
			line, _ := bufio.NewReader(io.LimitReader(res.Body, 4096)).ReadString('\n')
			res.Body.Close()
			want = strings.ToLower(strings.TrimSpace(strings.SplitN(line, " ", 2)[0]))
		}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, av.URL, nil)
	if err != nil {
		return nil, err
	}
	res, err := s.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return nil, fmt.Errorf("download: HTTP %d", res.StatusCode)
	}
	h := sha256.New()
	staged, err := s.Root.StageSystemUpdate(ctx, path.Base(av.Name), av.Size, io.TeeReader(res.Body, h))
	if err != nil {
		return nil, err
	}
	if want != "" {
		if got := hex.EncodeToString(h.Sum(nil)); got != want {
			s.Root.DiscardSystemUpdate()
			return nil, fmt.Errorf("sha256 mismatch: published %s, downloaded %s", want, got)
		}
		staged.Warning = strings.TrimSpace(staged.Warning + " sha256 verified")
	} else if staged.Warning == "" {
		staged.Warning = "no .sha256 published for this asset; not verified"
	}
	if s.Log != nil {
		s.Log.Info("system update staged from the feed", "file", staged.File, "size", staged.Size)
	}
	return staged, nil
}
