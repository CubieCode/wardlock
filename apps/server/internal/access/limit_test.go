// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package access

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"wardlock.nz/server/internal/store"
	"wardlock.nz/server/internal/store/storetest"
)

func TestLimiter(t *testing.T) {
	now := time.Date(2026, 10, 18, 9, 0, 0, 0, time.UTC)
	l := newLimiter(func() time.Time { return now })
	r := Rate{N: 2, Per: time.Minute}
	for i := range 2 {
		if ok, _ := l.allow("a", r); !ok {
			t.Fatalf("event %d refused", i+1)
		}
	}
	if over, wait := l.exceeded("a", r); !over || wait != time.Minute {
		t.Fatalf("exceeded = %v %v after N events", over, wait)
	}
	if ok, _ := l.allow("a", r); ok {
		t.Fatal("event N+1 allowed")
	}
	now = now.Add(time.Minute)
	if over, _ := l.exceeded("a", r); over {
		t.Fatal("limit did not reset")
	}
}

// TestLimiterOverflow: a full bounded table counts a new key in the
// overflow sketch, under the same limit, instead of refusing it; a key
// that later moves into the table keeps its count; expired windows are
// evicted; the server-key limiter is never full.
func TestLimiterOverflow(t *testing.T) {
	now := time.Date(2026, 10, 18, 9, 0, 0, 0, time.UTC)
	l := newLimiter(func() time.Time { return now })
	l.maxKeys = 2
	short := Rate{N: 5, Per: 30 * time.Second}
	r := Rate{N: 5, Per: 10 * time.Minute}
	l.allow("a", short)
	l.allow("b", short)
	for i := range 3 {
		if ok, _ := l.allow("c", r); !ok {
			t.Fatalf("overflow event %d refused", i+1)
		}
	}
	if n, wait := l.count("c", r); n != 3 || wait != r.Per {
		t.Fatalf("overflow count = %d %v, want 3 %v", n, wait, r.Per)
	}
	if over, _ := l.exceeded("d", r); over {
		t.Fatal("an unseen key was reported over")
	}
	if _, held := l.windows["c"]; held {
		t.Fatal("the full table took a new key")
	}
	if ok, _ := l.allow("a", short); !ok {
		t.Fatal("a tracked key under its limit was refused")
	}

	// a and b expire and are swept; c moves into the table with its
	// overflow count, so moving resets nothing.
	now = now.Add(time.Minute)
	for i := range 2 {
		if ok, _ := l.allow("c", r); !ok {
			t.Fatalf("event %d after moving refused", i+4)
		}
	}
	if w, held := l.windows["c"]; !held || w.n != 5 {
		t.Fatalf("c in the table = %+v %v, want 5 events", w, held)
	}
	if ok, wait := l.allow("c", r); ok || wait <= 0 {
		t.Fatal("c's sixth event in one window was allowed")
	}
	if len(l.windows) != 1 {
		t.Fatalf("expired windows kept: %v", l.windows)
	}

	u := newServerKeyLimiter(func() time.Time { return now })
	for i := range defaultMaxKeys + 10 {
		if ok, _ := u.allow(fmt.Sprintf("site-%d", i), r); !ok {
			t.Fatalf("the unbounded limiter refused key %d", i)
		}
	}
	if len(u.sketches) != 0 {
		t.Fatal("the unbounded limiter made a sketch")
	}
}

// TestOverflowEnforcesLimits: keys counted only in the sketch are held to
// their limits exactly as tracked keys are, and are reset with the window.
func TestOverflowEnforcesLimits(t *testing.T) {
	now := time.Date(2026, 10, 18, 9, 0, 0, 0, time.UTC)
	l := newLimiter(func() time.Time { return now })
	l.maxKeys = 1
	r := Rate{N: 4, Per: 15 * time.Minute}
	l.allow("held", r)
	for i := range 200 {
		key := fmt.Sprintf("password-ip:2001:db8:%x:%x::/64", i/16, i%16)
		for j := range r.N {
			if ok, _ := l.allow(key, r); !ok {
				t.Fatalf("%s event %d refused", key, j+1)
			}
		}
		if ok, wait := l.allow(key, r); ok || wait <= 0 || wait > r.Per {
			t.Fatalf("%s over its limit: %v %v", key, ok, wait)
		}
		if over, _ := l.exceeded(key, r); !over {
			t.Fatalf("%s not reported over", key)
		}
	}
	now = now.Add(r.Per)
	if ok, _ := l.allow("password-ip:2001:db8::/64", r); !ok {
		t.Fatal("the overflow window did not reset")
	}
}

// TestSketchNeverUndercounts: however keys collide, a key's estimate is
// never below its true count, so the sketch cannot lift a limit.
func TestSketchNeverUndercounts(t *testing.T) {
	l := newLimiter(time.Now)
	sk := newSketch(64)
	want := map[string]int{}
	for i := range 5000 {
		key := fmt.Sprintf("email-send-address:v%d@example.test", (i*7919)%700)
		sk.add(l.seed, key)
		want[key]++
	}
	for key, n := range want {
		if got := sk.estimate(l.seed, key); got < n {
			t.Fatalf("%s estimate %d below its count %d", key, got, n)
		}
	}
}

// TestSketchSaturation: a flood well below N x width events leaves unseen
// keys alone, while one far above it refuses them: memory stays fixed and
// the sketch fails closed rather than lifting a limit.
func TestSketchSaturation(t *testing.T) {
	const width, n = 1024, 5
	l := newLimiter(time.Now)
	refused := func(sk *sketch) int {
		c := 0
		for i := range 1000 {
			if sk.estimate(l.seed, fmt.Sprintf("probe-%d", i)) >= n {
				c++
			}
		}
		return c
	}
	sk := newSketch(width)
	for i := range n * width / 4 {
		sk.add(l.seed, fmt.Sprintf("flood-%d", i))
	}
	if c := refused(sk); c > 10 {
		t.Fatalf("%d of 1000 unseen keys refused after a flood of N x width / 4", c)
	}
	for i := range 8 * n * width {
		sk.add(l.seed, fmt.Sprintf("flood-more-%d", i))
	}
	if c := refused(sk); c < 990 {
		t.Fatalf("only %d of 1000 unseen keys refused after a flood of 8 x N x width", c)
	}
}

// TestOverflowSketchPerMethod: methods keep separate sketches, so
// saturating one method's counters refuses nobody on another.
func TestOverflowSketchPerMethod(t *testing.T) {
	now := time.Date(2026, 10, 18, 9, 0, 0, 0, time.UTC)
	l := newLimiter(func() time.Time { return now })
	l.maxKeys, l.sketchWidth = 1, 64
	r := Rate{N: 3, Per: 15 * time.Minute}
	l.allow("held", r)
	for i := range 8 * r.N * 64 {
		l.allow(fmt.Sprintf("link-fail-ip:2001:db8:%x::/64", i), r)
	}
	if ok, _ := l.allow("link-fail-ip:192.0.2.1", r); ok {
		t.Fatal("the saturated method's sketch let an unseen key in")
	}
	for i := range 100 {
		if ok, _ := l.allow(fmt.Sprintf("password-ip:198.51.100.%d", i), r); !ok {
			t.Fatalf("another method's unseen key %d refused", i)
		}
	}
	if len(l.sketches) != 2 {
		t.Fatalf("%d sketches, want one per method", len(l.sketches))
	}
}

func TestNetKey(t *testing.T) {
	for _, tc := range []struct{ ip, want string }{
		{"192.0.2.77", "192.0.2.0/24"},
		{"::ffff:192.0.2.77", "192.0.2.0/24"},
		{"2001:db8:aa:bb:cc::1", "2001:db8:aa::/48"},
		{"2001:db8:aa:ffff::9", "2001:db8:aa::/48"},
		{"fe80::1%eth0", "fe80::/48"},
		{"not-an-ip", "not-an-ip"},
	} {
		if got := netKey(tc.ip); got != tc.want {
			t.Errorf("netKey(%q) = %q, want %q", tc.ip, got, tc.want)
		}
	}
	for _, tc := range []struct{ limit, share, mark int }{{100, 10, 50}, {200, 20, 100}, {300, 30, 150}, {1000, 100, 500}, {5, 1, 3}} {
		if got := networkShare(tc.limit); got != tc.share {
			t.Errorf("networkShare(%d) = %d, want %d", tc.limit, got, tc.share)
		}
		if underPressure(tc.mark-1, tc.limit) || !underPressure(tc.mark, tc.limit) {
			t.Errorf("pressure mark of %d is not %d", tc.limit, tc.mark)
		}
	}
}

// syncNotifier delivers at once, so policy changes drop the cache.
type syncNotifier struct{ subs []func(string) }

func (n *syncNotifier) Notify(_ context.Context, siteID string) {
	for _, fn := range n.subs {
		fn(siteID)
	}
}

func (n *syncNotifier) Subscribe(fn func(string)) func() {
	n.subs = append(n.subs, fn)
	return func() {}
}

// stubProvider is an OAuth provider that only builds authorisation URLs.
type stubProvider struct{}

func (stubProvider) ID() string        { return "stub" }
func (stubProvider) Name() string      { return "Stub" }
func (stubProvider) GitHub() bool      { return false }
func (stubProvider) Origins() []string { return []string{"https://id.example.test"} }
func (stubProvider) AuthCodeURL(context.Context, AuthRequest) (string, error) {
	return "https://id.example.test/authorize", nil
}

func (stubProvider) Identify(context.Context, Exchange) (Identity, error) {
	return Identity{}, errors.New("stub provider")
}

const floodPassword = "harbour-lights-7"

// flood is a service on a test clock with one protected site, "harbour",
// whose default policy enables every method, and a share link to it.
// Methods are limited the same whether an address is allowed or not.
type flood struct {
	t      *testing.T
	s      *Service
	siteID string
	token  string

	mu  sync.Mutex // background email sends read the clock
	now time.Time
}

func (f *flood) clock() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.now
}

func (f *flood) advance(d time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.now = f.now.Add(d)
}

func newFlood(t *testing.T, limits Limits) *flood {
	t.Helper()
	f := &flood{t: t, now: time.Date(2026, 10, 18, 9, 0, 0, 0, time.UTC)}
	db := storetest.New(t)
	s, err := New(Config{
		Sites: db.Sites(), Policies: db.Policies(), Sessions: db.Sessions(), Links: db.Links(), PasswordSessions: db.PasswordSessions(), EmailCodes: db.EmailCodes(), Notifier: &syncNotifier{},
		Secret: []byte("limit-test-server-key-0123456789abcdef"), Now: f.clock,
		OAuth:  OAuthConfig{Providers: []Provider{stubProvider{}}, AuthOrigin: "https://auth.example.test", SiteOrigin: func(h string) string { return "https://" + h }},
		Limits: limits,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	// A cheap stand-in for Argon2id: these tests count attempts, not
	// hashes.
	s.derive = func(pw, salt []byte, _, _ uint32, _ uint8, n uint32) []byte {
		return append(append([]byte{}, pw...), salt...)[:min(int(n), len(pw)+len(salt))]
	}
	o := store.Owner{ID: store.NewID(), Email: "owner@example.test", CreatedAt: f.now}
	site := store.Site{ID: store.NewID(), OwnerID: o.ID, Name: "harbour", CreatedAt: f.now}
	if err := db.Owners().Create(t.Context(), o); err != nil {
		t.Fatal(err)
	}
	if err := db.Sites().Create(t.Context(), site); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Set(t.Context(), o.ID, "harbour", Rule{
		PathPrefix: "/", Visibility: store.Protected, Links: true, Password: true, NewPassword: floodPassword,
		// No flood address is allowed, so sends store and mail nothing.
		Email: true, OAuth: true, Allowlist: &store.Allowlist{Domains: []string{"crew.example.test"}},
	}); err != nil {
		t.Fatal(err)
	}
	_, f.token, err = s.CreateLink(t.Context(), o.ID, "harbour", NewLink{})
	if err != nil {
		t.Fatal(err)
	}
	f.s, f.siteID = s, site.ID
	return f
}

func (f *flood) password(ip, pw string) error {
	_, err := f.s.PasswordLogin(f.t.Context(), PasswordAttempt{SiteID: f.siteID, Path: "/", IP: ip, Password: pw})
	return err
}

func (f *flood) send(ip, addr string) error {
	return f.s.SendEmailCode(f.t.Context(), EmailCodeRequest{SiteID: f.siteID, Site: "harbour.example.test", Path: "/", IP: ip, Address: addr})
}

func (f *flood) verify(ip, addr string) error {
	_, err := f.s.EmailCodeLogin(f.t.Context(), EmailCodeAttempt{SiteID: f.siteID, Path: "/", IP: ip, Address: addr, Code: "000000"})
	return err
}

func (f *flood) oauth(ip string) error {
	_, err := f.s.StartOAuth(f.t.Context(), OAuthStart{SiteID: f.siteID, Host: "harbour.example.test", Path: "/", Provider: "stub", IP: ip})
	return err
}

func (f *flood) guess(ip string) Decision {
	d, err := f.s.Decide(f.t.Context(), Request{SiteID: f.siteID, Path: "/", Token: "wl_junk_guess", ClientIP: ip})
	if err != nil {
		f.t.Fatal(err)
	}
	return d
}

func (f *flood) open(ip string) Decision {
	d, err := f.s.Decide(f.t.Context(), Request{SiteID: f.siteID, Path: "/", Token: f.token, ClientIP: ip})
	if err != nil {
		f.t.Fatal(err)
	}
	return d
}

func limited(err error) bool { return errors.Is(err, ErrRateLimited) }

// fillIPTable fills the visitor per-IP table, shrunk to n keys, with
// password guesses from n IPv6 /64s in one /48: as an attacker with a
// single /48 can.
func (f *flood) fillIPTable(n int) {
	f.t.Helper()
	f.s.limiter.maxKeys = n
	for i := range n {
		_ = f.password(fmt.Sprintf("2001:db8:ff:%x::1", i), "wrong-guess")
	}
	if !f.s.limiter.full() {
		f.t.Fatalf("the IP table holds %d of %d keys", len(f.s.limiter.windows), n)
	}
}

// TestFullIPTableKeepsSignInOpen: with the visitor per-IP table full,
// clients it does not hold still sign in by every method, and each is
// still held to every per-IP limit, now counted in the overflow sketch.
func TestFullIPTableKeepsSignInOpen(t *testing.T) {
	f := newFlood(t, Limits{})
	f.fillIPTable(64)
	lim := f.s.limits

	if err := f.password("192.0.2.10", floodPassword); err != nil {
		t.Fatalf("password from an unseen client: %v", err)
	}
	if err := f.send("192.0.2.11", "visitor@example.test"); err != nil {
		t.Fatalf("email send from an unseen client: %v", err)
	}
	if err := f.verify("192.0.2.12", "visitor@example.test"); !errors.Is(err, ErrInvalidCode) {
		t.Fatalf("code entry from an unseen client: %v", err)
	}
	if err := f.oauth("192.0.2.13"); err != nil {
		t.Fatalf("OAuth start from an unseen client: %v", err)
	}
	if d := f.open("192.0.2.14"); d.Outcome != Allow || !d.TokenAccepted {
		t.Fatalf("share link from an unseen client: %+v", d)
	}
	if d := f.guess("192.0.2.15"); d.Outcome == RateLimited {
		t.Fatalf("one wrong token from an unseen client was limited: %+v", d)
	}

	// No per-IP limit is lifted for clients the table does not hold.
	for _, tc := range []struct {
		name string
		r    Rate
		try  func(ip string, i int) bool // reports whether it was limited
	}{
		{"password", lim.PasswordPerIP, func(ip string, _ int) bool { return limited(f.password(ip, "wrong-guess")) }},
		{"email send", lim.EmailSendPerIP, func(ip string, i int) bool { return limited(f.send(ip, fmt.Sprintf("v%d@example.test", i))) }},
		{"email entry", lim.EmailVerifyPerIP, func(ip string, i int) bool { return limited(f.verify(ip, fmt.Sprintf("v%d@example.test", i))) }},
		{"OAuth start", lim.OAuthStartsPerIP, func(ip string, _ int) bool { return limited(f.oauth(ip)) }},
		{"token guess", lim.TokenFailuresPerIP, func(ip string, _ int) bool { return f.guess(ip).Outcome == RateLimited }},
	} {
		ip := "2001:db8:1:" + fmt.Sprint(len(tc.name)) + "::7"
		for i := range tc.r.N {
			if tc.try(ip, i) {
				t.Fatalf("%s %d from an unseen client limited", tc.name, i+1)
			}
		}
		if !tc.try(ip, tc.r.N) {
			t.Fatalf("%s: an unseen client went past its per-IP limit of %d", tc.name, tc.r.N)
		}
		if _, held := f.s.limiter.windows[ipKey(ip)]; held {
			t.Fatalf("%s: the full table took a new key", tc.name)
		}
	}
	// A client the table holds keeps its count.
	for range lim.PasswordPerIP.N {
		_ = f.password("2001:db8:ff:3::1", "wrong-guess")
	}
	if err := f.password("2001:db8:ff:3::1", floodPassword); !limited(err) {
		t.Fatalf("a held client over its limit: %v", err)
	}
}

// TestFullIPTableKeepsSiteLimits: guesses from addresses the full IP table
// does not hold still count against the site and close it at its limit.
func TestFullIPTableKeepsSiteLimits(t *testing.T) {
	f := newFlood(t, Limits{})
	f.fillIPTable(8)
	r := f.s.limits.TokenFailuresPerSite
	// From enough networks that none goes over its share.
	for i := range r.N {
		if d := f.guess(fmt.Sprintf("2001:db8:%x::1", 0x100+i/10)); d.Outcome == RateLimited {
			t.Fatalf("guess %d limited before the site limit", i+1)
		}
	}
	if d := f.open("198.51.100.1"); d.Outcome != RateLimited || d.RetryAfter <= 0 {
		t.Fatalf("the site limit with a full IP table: %+v", d)
	}
}

// TestFullAddressTableKeepsEmailOpen: inventing addresses until the
// per-address table is full leaves email codes open for addresses it does
// not hold, each still held to its per-address limits.
func TestFullAddressTableKeepsEmailOpen(t *testing.T) {
	f := newFlood(t, Limits{})
	f.s.addressLimiter.maxKeys = 30
	for i := range 30 {
		if err := f.send(fmt.Sprintf("2001:db8:%x::1", i), fmt.Sprintf("invented%d@example.test", i)); err != nil {
			t.Fatalf("send %d: %v", i, err)
		}
	}
	if !f.s.addressLimiter.full() {
		t.Fatal("the address table is not full")
	}
	r := f.s.limits.EmailSendPerAddress
	for i := range r.N {
		if err := f.send(fmt.Sprintf("2001:db8:%x::1", 0x100+i), "visitor@example.test"); err != nil {
			t.Fatalf("send %d to an unseen address: %v", i+1, err)
		}
	}
	if err := f.send("2001:db8:1ff::1", "visitor@example.test"); !limited(err) {
		t.Fatalf("an unseen address went past its limit: %v", err)
	}
	v := f.s.limits.EmailVerifyPerAddress
	for i := range v.N {
		if err := f.verify(fmt.Sprintf("2001:db8:%x::1", 0x200+i), "other@example.test"); !errors.Is(err, ErrInvalidCode) {
			t.Fatalf("entry %d for an unseen address: %v", i+1, err)
		}
	}
	if err := f.verify("2001:db8:2ff::1", "other@example.test"); !limited(err) {
		t.Fatalf("an unseen address went past its entry limit: %v", err)
	}
}

// siteMethod is one per-site limit, driven from a given IP.
type siteMethod struct {
	name string
	r    func(Limits) Rate
	// try makes the i-th attempt from ip and reports whether it was
	// limited.
	try func(f *flood, ip string, i int) bool
}

var siteMethods = []siteMethod{
	{"password", func(l Limits) Rate { return l.PasswordPerSite }, func(f *flood, ip string, _ int) bool { return limited(f.password(ip, "wrong-guess")) }},
	{"email send", func(l Limits) Rate { return l.EmailSendPerSite }, func(f *flood, ip string, i int) bool {
		return limited(f.send(ip, fmt.Sprintf("v%d@example.test", i)))
	}},
	{"email entry", func(l Limits) Rate { return l.EmailVerifyPerSite }, func(f *flood, ip string, i int) bool {
		return limited(f.verify(ip, fmt.Sprintf("v%d@example.test", i)))
	}},
	{"token guess", func(l Limits) Rate { return l.TokenFailuresPerSite }, func(f *flood, ip string, _ int) bool { return f.guess(ip).Outcome == RateLimited }},
	{"OAuth start", func(Limits) Rate { return Rate{N: maxOAuthEntriesPerSite, Per: OAuthFlowLifetime} }, func(f *flood, ip string, _ int) bool { return limited(f.oauth(ip)) }},
}

// generous lifts the per-IP and per-address limits out of the way, so a
// test reaches the per-site limits and network shares from few addresses.
func generous() Limits {
	l := DefaultLimits()
	big := func(r Rate) Rate { return Rate{N: 1 << 20, Per: r.Per} }
	l.PasswordPerIP, l.EmailSendPerIP, l.EmailVerifyPerIP = big(l.PasswordPerIP), big(l.EmailSendPerIP), big(l.EmailVerifyPerIP)
	l.EmailSendPerAddress, l.EmailVerifyPerAddress = big(l.EmailSendPerAddress), big(l.EmailVerifyPerAddress)
	l.OAuthStartsPerIP, l.TokenFailuresPerIP = big(l.OAuthStartsPerIP), big(l.TokenFailuresPerIP)
	return l
}

// TestNetworkShareOnlyUnderPressure: below a site's pressure mark, thirty
// clients behind one IPv6 /48 or one IPv4 /24 each get their full per-IP
// allowance on one site, more than any network's share.
func TestNetworkShareOnlyUnderPressure(t *testing.T) {
	t.Parallel()
	limits := DefaultLimits()
	limits.PasswordPerSite = Rate{N: 1000, Per: 15 * time.Minute}
	limits.EmailSendPerSite = Rate{N: 2000, Per: time.Hour}
	limits.EmailVerifyPerSite = Rate{N: 3000, Per: 15 * time.Minute}
	limits.TokenFailuresPerSite = Rate{N: 3000, Per: 15 * time.Minute}
	for _, net := range []struct {
		name string
		ip   func(i int) string
	}{
		{"one /48", func(i int) string { return fmt.Sprintf("2001:db8:7:%x::1", i) }},
		{"one /24", func(i int) string { return fmt.Sprintf("192.0.2.%d", i+1) }},
	} {
		for _, m := range siteMethods[:4] {
			t.Run(net.name+" "+m.name, func(t *testing.T) {
				t.Parallel()
				f := newFlood(t, limits)
				perIP := map[string]Rate{
					"password": limits.PasswordPerIP, "email send": limits.EmailSendPerIP,
					"email entry": limits.EmailVerifyPerIP, "token guess": limits.TokenFailuresPerIP,
				}[m.name]
				if 30*perIP.N <= networkShare(m.r(limits).N) || 30*perIP.N*sitePressureDivisor >= m.r(limits).N {
					t.Fatal("the test does not exceed a share below the pressure mark")
				}
				k := 0
				for c := range 30 {
					for range perIP.N {
						if m.try(f, net.ip(c), k) {
							t.Fatalf("client %d attempt %d limited below the pressure mark", c, k)
						}
						k++
					}
				}
			})
		}
	}
}

// TestNetworkShareUnderPressure: once a site is under pressure, one
// network gets no more than its share of the site's limit in the window,
// counting what it spent before, and attempts over its share spend nothing
// of the site's; other networks still get in. A network that spent right
// up to the pressure mark alone (more than its share) gets nothing more.
func TestNetworkShareUnderPressure(t *testing.T) {
	t.Parallel()
	limits := generous()
	for _, m := range siteMethods {
		r := m.r(limits)
		share, mark := networkShare(r.N), pressureMark(r.N)
		for _, early := range []int{share / 2, mark} {
			t.Run(fmt.Sprintf("%s after %d", m.name, early), func(t *testing.T) {
				t.Parallel()
				f := newFlood(t, limits)
				k := 0
				// The flooding /48 spends early attempts before the
				// pressure.
				for i := range early {
					if m.try(f, fmt.Sprintf("2001:db8:a:%x::1", i), k) {
						t.Fatalf("attempt %d limited before the pressure mark", i+1)
					}
					k++
				}
				// Other networks, each within its share, bring the site
				// to the pressure mark.
				for i := range mark - early {
					if m.try(f, fmt.Sprintf("2001:db8:%x::1", 0x100+i/max(1, share-1)), k) {
						t.Fatalf("attempt %d from a network within its share limited", i)
					}
					k++
				}
				// The flooding /48, from fresh /64s, gets the rest of its
				// share, if any, and no more.
				got := 0
				for i := range 3 * share {
					if !m.try(f, fmt.Sprintf("2001:db8:a:%x::1", 0x1000+i), k) {
						got++
					}
					k++
				}
				if early+got != max(early, share) {
					t.Fatalf("one network spent %d by the end of the window, want %d", early+got, max(early, share))
				}
				// Its refused attempts did not spend the site's limit:
				// fresh networks still have r.N - mark - got left.
				for i := range r.N - mark - got {
					if m.try(f, fmt.Sprintf("2001:db8:%x::1", 0x2000+i/max(1, share-1)), k) {
						t.Fatalf("attempt %d of the site's remainder limited", i)
					}
					k++
				}
				if !m.try(f, "2001:db8:ffff::1", k) {
					t.Fatalf("the site went past its limit of %d", r.N)
				}
			})
		}
	}
}

// pressureMark is the count at which limit puts a site under pressure.
func pressureMark(limit int) int { return (limit + sitePressureDivisor - 1) / sitePressureDivisor }

// TestSiteFloodReopens models the adaptive attacker. One network spends a
// site's limit up to the pressure mark alone, then five more each spend
// their share: six networks fill the site and hold the method shut window
// after window. With five networks, in any arrangement, the method
// reopens within one window. Ten networks at their shares from the start
// also fill it, and more networks still close the site at its limit.
func TestSiteFloodReopens(t *testing.T) {
	t.Parallel()
	limits := generous()
	for _, m := range siteMethods {
		t.Run(m.name, func(t *testing.T) {
			t.Parallel()
			f := newFlood(t, limits)
			r := m.r(limits)
			share, mark := networkShare(r.N), pressureMark(r.N)
			// 1 + (1 - 1/sitePressureDivisor) x networkShareDivisor.
			need := 1 + (r.N-mark+share-1)/share
			if need != 6 {
				t.Fatalf("%d networks fill the site, want 6 with the default constants", need)
			}
			k, base := 0, 0
			// spend makes up to n attempts from fresh /64s of network
			// net and returns how many were let through.
			spend := func(net, n int) int {
				got := 0
				for range n {
					if !m.try(f, fmt.Sprintf("2001:db8:%x:%x::1", net, k), k) {
						got++
					}
					k++
				}
				return got
			}
			// flood runs one window: network i first spends pre[i]
			// (before the pressure, when they add up to no more than the
			// mark), then every network tries as much as it can. It
			// returns what the site let through.
			flood := func(pre []int) int {
				base += 0x100
				total := 0
				for i, n := range pre {
					got := spend(base+i, n)
					if got != n {
						t.Fatalf("%v: network %d got %d of %d before the pressure mark", pre, i, got, n)
					}
					total += got
				}
				// No network can get more than the mark plus its share.
				for i := range pre {
					total += spend(base+i, mark+share)
				}
				return total
			}
			open := func() bool { k++; return !m.try(f, fmt.Sprintf("192.0.2.%d", k%250+1), k) }

			// Six networks, one up to the mark: shut, window after window.
			adaptive := append([]int{mark}, make([]int, need-1)...)
			for w := range 3 {
				if got := flood(adaptive); got != r.N {
					t.Fatalf("window %d: six networks spent %d, want the site limit %d", w+1, got, r.N)
				}
				if open() {
					t.Fatalf("window %d: six networks did not hold the method shut", w+1)
				}
				f.advance(r.Per)
			}
			// Five networks, however they spread their attempts before
			// the pressure: the method stays open.
			for _, pre := range [][]int{
				{mark, 0, 0, 0, 0},
				{mark - 1, 1, 0, 0, 0},
				{mark / 2, mark - mark/2, 0, 0, 0},
				{share, share, share, share, mark - 4*share},
				{0, 0, 0, 0, 0},
			} {
				if got := flood(pre); got >= r.N {
					t.Fatalf("five networks %v spent %d, the site limit", pre, got)
				}
				if !open() {
					t.Fatalf("five networks %v held the method shut", pre)
				}
				f.advance(r.Per)
			}
			// Ten networks at their shares from the start fill the site;
			// twenty still close it at r.N.
			if got := flood(make([]int, networkShareDivisor)); got != r.N || open() {
				t.Fatalf("ten networks spent %d, want the site limit %d and the method shut", got, r.N)
			}
			f.advance(r.Per)
			if got := flood(make([]int, 2*networkShareDivisor)); got != r.N {
				t.Fatalf("twenty networks spent %d, want the site limit %d", got, r.N)
			}
		})
	}
}

// TestFloodedNetworkTableKeepsShares: the per-network table is bounded
// like the per-IP one; when inventing networks fills it, unseen networks
// are counted in its sketch and still held to their shares.
func TestFloodedNetworkTableKeepsShares(t *testing.T) {
	limits := generous()
	f := newFlood(t, limits)
	f.s.netLimiter.maxKeys = 16
	r := limits.PasswordPerSite
	for i := range r.N / 2 {
		_ = f.password(fmt.Sprintf("2001:db8:%x::1", 0x300+i), "wrong-guess")
	}
	if !f.s.netLimiter.full() {
		t.Fatal("the network table is not full")
	}
	got := 0
	for i := range 3 * networkShare(r.N) {
		if !limited(f.password(fmt.Sprintf("2001:db8:b:%x::1", i), "wrong-guess")) {
			got++
		}
	}
	if got != networkShare(r.N) {
		t.Fatalf("an unseen network spent %d under pressure, want %d", got, networkShare(r.N))
	}
}
