// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

// Package platform holds process-wide concerns: configuration, logging,
// health and graceful shutdown. It imports no other internal package.
package platform

import (
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/mail"
	"net/netip"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Config is the server configuration, read once at startup from WARDLOCK_*
// environment variables (see .env.example for every variable).
type Config struct {
	// DataDir holds the SQLite database and, later, blobs and certificates.
	DataDir string
	// HTTPAddr is the listen address for plain HTTP: the edge and API, or
	// in auto TLS mode ACME HTTP-01 answers and the redirect to HTTPS.
	HTTPAddr string
	// OperatorAddr is the listen address for /healthz, /readyz and
	// /metrics (WARDLOCK_OPERATOR_ADDR); empty serves none of them there.
	OperatorAddr string
	// TLS is the TLS mode and its settings.
	TLS TLSConfig
	// BaseDomain is the domain sites are published under: <site>.<BaseDomain>.
	BaseDomain string
	// DashboardHost serves the dashboard and the owner API.
	DashboardHost string
	// DevDashboardURL, when set, proxies dashboard pages to a Vite dev server.
	DevDashboardURL *url.URL
	LogLevel        slog.Level
	LogFormat       string
	// SMTP is outgoing mail: WARDLOCK_SMTP_URL and WARDLOCK_SMTP_FROM.
	SMTP SMTPConfig
	// BlobBackend is where deploy file bodies live: BlobFS (the default)
	// or BlobS3.
	BlobBackend string
	// BlobDir holds them for BlobFS; <DataDir>/blobs by default, and ""
	// for BlobS3.
	BlobDir string
	// S3 is the bucket for BlobS3.
	S3 S3Config
	// BlobCacheDir keeps local copies of served blobs, at most
	// BlobCacheMaxBytes of them; "" (the default) caches nothing.
	BlobCacheDir      string
	BlobCacheMaxBytes int64
	// Deploy limits: bytes per file, files per deploy, bytes per deploy.
	MaxFileSize    int64
	MaxDeployFiles int
	MaxDeploySize  int64
	// MaxVideoSize is the per-file limit for video files; 0 refuses them
	// (A2.14).
	MaxVideoSize int64
	// BlockedExtensions are the lowercase extensions, each with its dot,
	// that no deploy may contain (WARDLOCK_BLOCKED_EXTENSIONS); empty
	// blocks none.
	BlockedExtensions []string
	// Per-owner quotas (A2.14), which `wardlockd admin quota` can override
	// for one owner: bytes stored, sites, and deploys in any 24 hours. 0 is
	// unlimited, the default for all three.
	MaxOwnerStorage       int64
	MaxOwnerSites         int64
	MaxOwnerDeploysPerDay int64
	// Access log: client IPs keep only the first AccessLogIPv4Prefix or
	// AccessLogIPv6Prefix bits (0 stores none), entries older than
	// AccessLogRetention are pruned (0 keeps them forever) and at most
	// AccessLogBuffer entries wait for a write.
	AccessLogIPv4Prefix int
	AccessLogIPv6Prefix int
	AccessLogRetention  time.Duration
	AccessLogBuffer     int
	// Garbage collection: GCInterval between runs in serve (0 runs none;
	// `wardlockd gc` still works), GCGrace protecting recent uploads and
	// pending deploys, DeployHistory deploys kept per site.
	GCInterval    time.Duration
	GCGrace       time.Duration
	DeployHistory int
	// DNSResolvers are the host:port DNS servers custom-domain verification
	// asks (WARDLOCK_DNS_RESOLVERS); empty means the system resolvers.
	DNSResolvers []string
	// OAuth is the operator's OAuth and OIDC providers (A2.5).
	OAuth OAuthConfig
	// Invites is who may invite owners (WARDLOCK_INVITES, A2.13): "owners"
	// (the default) or "operator", leaving it to `wardlockd admin create`.
	Invites string
	// AbuseEmail receives visitors' abuse reports (WARDLOCK_ABUSE_EMAIL,
	// A2.13); "" shows no report link and serves no report form.
	AbuseEmail string
	// Moderation is content screening (A2.15); off unless configured.
	Moderation ModerationConfig
	// Secret is the decoded WARDLOCK_SECRET: the server key for token,
	// code and session hashes. Empty when unset; commands that need it
	// call RequireSecret.
	Secret []byte
}

// MinSecretBytes is the shortest WARDLOCK_SECRET accepted, after decoding.
const MinSecretBytes = 32

// ErrNoSecret means WARDLOCK_SECRET is unset where it is required.
var ErrNoSecret = fmt.Errorf("%w: WARDLOCK_SECRET is required: at least %d random bytes, base64url-encoded (for example `openssl rand -base64 32 | tr '+/' '-_'`)", ErrInvalidConfig, MinSecretBytes)

// RequireSecret returns ErrNoSecret when WARDLOCK_SECRET is unset.
func (c Config) RequireSecret() error {
	if len(c.Secret) == 0 {
		return ErrNoSecret
	}
	return nil
}

// ErrInvalidConfig wraps every configuration error.
var ErrInvalidConfig = errors.New("invalid configuration")

// LoadConfig reads the configuration through getenv (os.Getenv in main;
// a map lookup in tests) and validates it.
func LoadConfig(getenv func(string) string) (Config, error) {
	get := func(key, fallback string) string {
		if v := strings.TrimSpace(getenv(key)); v != "" {
			return v
		}
		return fallback
	}
	cfg := Config{
		DataDir:    get("WARDLOCK_DATA_DIR", "wardlock-data"),
		BaseDomain: normaliseHost(get("WARDLOCK_BASE_DOMAIN", "wardlock.localhost")),
		LogFormat:  get("WARDLOCK_LOG_FORMAT", "json"),
	}
	cfg.DashboardHost = normaliseHost(get("WARDLOCK_DASHBOARD_HOST", cfg.BaseDomain))

	if !validHost(cfg.BaseDomain) {
		return Config{}, fmt.Errorf("%w: WARDLOCK_BASE_DOMAIN %q is not a hostname", ErrInvalidConfig, cfg.BaseDomain)
	}
	if !validHost(cfg.DashboardHost) {
		return Config{}, fmt.Errorf("%w: WARDLOCK_DASHBOARD_HOST %q is not a hostname", ErrInvalidConfig, cfg.DashboardHost)
	}
	tlsCfg, err := parseTLSConfig(get, cfg.BaseDomain, cfg.DashboardHost)
	if err != nil {
		return Config{}, err
	}
	cfg.TLS = tlsCfg
	httpDefault := "127.0.0.1:8080"
	if cfg.TLS.Mode == TLSAuto {
		httpDefault = ":80"
	}
	cfg.HTTPAddr = get("WARDLOCK_HTTP_ADDR", httpDefault)
	if _, _, err := net.SplitHostPort(cfg.HTTPAddr); err != nil {
		return Config{}, fmt.Errorf("%w: WARDLOCK_HTTP_ADDR %q: %w", ErrInvalidConfig, cfg.HTTPAddr, err)
	}
	if cfg.OperatorAddr = get("WARDLOCK_OPERATOR_ADDR", ""); cfg.OperatorAddr != "" {
		if _, _, err := net.SplitHostPort(cfg.OperatorAddr); err != nil {
			return Config{}, fmt.Errorf("%w: WARDLOCK_OPERATOR_ADDR %q: %w", ErrInvalidConfig, cfg.OperatorAddr, err)
		}
		// Sharing a listener would put metrics on site and dashboard hosts.
		if cfg.OperatorAddr == cfg.HTTPAddr || (cfg.TLS.Mode == TLSAuto && cfg.OperatorAddr == cfg.TLS.HTTPSAddr) {
			return Config{}, fmt.Errorf("%w: WARDLOCK_OPERATOR_ADDR must differ from WARDLOCK_HTTP_ADDR and WARDLOCK_HTTPS_ADDR", ErrInvalidConfig)
		}
	}
	if err := cfg.LogLevel.UnmarshalText([]byte(get("WARDLOCK_LOG_LEVEL", "info"))); err != nil {
		return Config{}, fmt.Errorf("%w: WARDLOCK_LOG_LEVEL: %w", ErrInvalidConfig, err)
	}
	if cfg.LogFormat != "json" && cfg.LogFormat != "text" {
		return Config{}, fmt.Errorf("%w: WARDLOCK_LOG_FORMAT must be json or text", ErrInvalidConfig)
	}
	if raw := get("WARDLOCK_DEV_DASHBOARD_URL", ""); raw != "" {
		u, err := url.Parse(raw)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return Config{}, fmt.Errorf("%w: WARDLOCK_DEV_DASHBOARD_URL must be an http(s) URL", ErrInvalidConfig)
		}
		cfg.DevDashboardURL = u
	}
	smtp, err := parseSMTPConfig(get("WARDLOCK_SMTP_URL", "dev://"), get("WARDLOCK_SMTP_FROM", ""), cfg.DashboardHost)
	if err != nil {
		return Config{}, err
	}
	cfg.SMTP = smtp
	for _, l := range []struct {
		key      string
		fallback int64
		dst      func(int64)
	}{
		{"WARDLOCK_MAX_FILE_SIZE", 25 << 20, func(v int64) { cfg.MaxFileSize = v }},
		{"WARDLOCK_MAX_DEPLOY_FILES", 20_000, func(v int64) { cfg.MaxDeployFiles = int(v) }},
		{"WARDLOCK_MAX_DEPLOY_SIZE", 1 << 30, func(v int64) { cfg.MaxDeploySize = v }},
	} {
		v, err := positiveInt(get(l.key, ""), l.fallback)
		if err != nil {
			return Config{}, fmt.Errorf("%w: %s must be a positive whole number: %w", ErrInvalidConfig, l.key, err)
		}
		l.dst(v)
	}
	if err := cfg.loadQuotas(get); err != nil {
		return Config{}, err
	}
	if err := cfg.loadBlobs(get); err != nil {
		return Config{}, err
	}
	if err := cfg.loadAccessLog(get); err != nil {
		return Config{}, err
	}
	if err := cfg.loadGC(get); err != nil {
		return Config{}, err
	}
	if cfg.DNSResolvers, err = parseResolvers(get("WARDLOCK_DNS_RESOLVERS", "")); err != nil {
		return Config{}, err
	}
	if err := cfg.loadOAuth(get); err != nil {
		return Config{}, err
	}
	if err := cfg.loadModeration(get); err != nil {
		return Config{}, err
	}
	if err := cfg.loadOperator(get); err != nil {
		return Config{}, err
	}
	if raw := get("WARDLOCK_SECRET", ""); raw != "" {
		key, err := decodeSecret(raw)
		if err != nil {
			return Config{}, err
		}
		cfg.Secret = key
	}
	return cfg, nil
}

// loadQuotas reads the video limit, blocked extensions and per-owner quota
// variables (A2.14).
func (c *Config) loadQuotas(get func(key, fallback string) string) error {
	for _, l := range []struct {
		key      string
		fallback int64
		dst      *int64
	}{
		{"WARDLOCK_MAX_VIDEO_SIZE", 10 << 20, &c.MaxVideoSize},
		{"WARDLOCK_MAX_OWNER_STORAGE", 0, &c.MaxOwnerStorage},
		{"WARDLOCK_MAX_OWNER_SITES", 0, &c.MaxOwnerSites},
		{"WARDLOCK_MAX_OWNER_DEPLOYS_PER_DAY", 0, &c.MaxOwnerDeploysPerDay},
	} {
		raw := get(l.key, "")
		v, err := strconv.ParseInt(raw, 10, 64)
		switch {
		case raw == "":
			v = l.fallback
		case err != nil || v < 0:
			return fmt.Errorf("%w: %s must be a whole number, 0 or more", ErrInvalidConfig, l.key)
		}
		*l.dst = v
	}
	exts, err := ParseExtensions(get("WARDLOCK_BLOCKED_EXTENSIONS", ""))
	if err != nil {
		return fmt.Errorf("%w: WARDLOCK_BLOCKED_EXTENSIONS: %w", ErrInvalidConfig, err)
	}
	c.BlockedExtensions = exts
	return nil
}

// ParseExtensions reads a comma-separated list of file extensions, with or
// without their dots and in any letter case (exe, .MSI), and returns them
// lowercase with a dot, sorted and without duplicates.
func ParseExtensions(raw string) ([]string, error) {
	var out []string
	for item := range strings.SplitSeq(raw, ",") {
		item = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(item), "."))
		if item == "" {
			continue
		}
		if strings.ContainsFunc(item, func(r rune) bool { return (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '_' && r != '-' }) {
			return nil, fmt.Errorf("%q is not a file extension: use letters, digits, - and _, such as exe,msi,dmg", item)
		}
		out = append(out, "."+item)
	}
	slices.Sort(out)
	return slices.Compact(out), nil
}

// loadOperator reads the operator controls' variables (A2.13).
func (c *Config) loadOperator(get func(key, fallback string) string) error {
	switch c.Invites = strings.ToLower(get("WARDLOCK_INVITES", "owners")); c.Invites {
	case "owners", "operator":
	default:
		return fmt.Errorf("%w: WARDLOCK_INVITES must be owners or operator", ErrInvalidConfig)
	}
	if raw := get("WARDLOCK_ABUSE_EMAIL", ""); raw != "" {
		a, err := mail.ParseAddress(raw)
		if err != nil || a.Name != "" || a.Address != raw {
			return fmt.Errorf("%w: WARDLOCK_ABUSE_EMAIL must be a plain email address, such as abuse@example.com", ErrInvalidConfig)
		}
		c.AbuseEmail = a.Address
	}
	return nil
}

// loadAccessLog reads the WARDLOCK_ACCESS_LOG_* variables.
func (c *Config) loadAccessLog(get func(key, fallback string) string) error {
	for _, l := range []struct {
		key           string
		fallback, max int64
		dst           func(int64)
	}{
		{"WARDLOCK_ACCESS_LOG_IPV4_PREFIX", 24, 32, func(v int64) { c.AccessLogIPv4Prefix = int(v) }},
		{"WARDLOCK_ACCESS_LOG_IPV6_PREFIX", 48, 128, func(v int64) { c.AccessLogIPv6Prefix = int(v) }},
		{"WARDLOCK_ACCESS_LOG_RETENTION_DAYS", 90, 36_500, func(v int64) { c.AccessLogRetention = time.Duration(v) * 24 * time.Hour }},
	} {
		raw := get(l.key, "")
		v, err := strconv.ParseInt(raw, 10, 64)
		switch {
		case raw == "":
			v = l.fallback
		case err != nil || v < 0 || v > l.max:
			return fmt.Errorf("%w: %s must be a whole number from 0 to %d", ErrInvalidConfig, l.key, l.max)
		}
		l.dst(v)
	}
	buf, err := positiveInt(get("WARDLOCK_ACCESS_LOG_BUFFER", ""), 10_000)
	if err != nil || buf > 1_000_000 {
		return fmt.Errorf("%w: WARDLOCK_ACCESS_LOG_BUFFER must be a whole number from 1 to 1000000", ErrInvalidConfig)
	}
	c.AccessLogBuffer = int(buf)
	return nil
}

// loadGC reads the garbage collection variables.
func (c *Config) loadGC(get func(key, fallback string) string) error {
	for _, l := range []struct {
		key                string
		fallback, min, max int64
		dst                func(int64)
	}{
		{"WARDLOCK_GC_INTERVAL_MINUTES", 60, 0, 525_600, func(v int64) { c.GCInterval = time.Duration(v) * time.Minute }},
		{"WARDLOCK_GC_GRACE_HOURS", 24, 1, 8_760, func(v int64) { c.GCGrace = time.Duration(v) * time.Hour }},
		{"WARDLOCK_DEPLOY_HISTORY", 50, 1, 100_000, func(v int64) { c.DeployHistory = int(v) }},
	} {
		raw := get(l.key, "")
		v, err := strconv.ParseInt(raw, 10, 64)
		switch {
		case raw == "":
			v = l.fallback
		case err != nil || v < l.min || v > l.max:
			return fmt.Errorf("%w: %s must be a whole number from %d to %d", ErrInvalidConfig, l.key, l.min, l.max)
		}
		l.dst(v)
	}
	return nil
}

// parseResolvers reads comma-separated host:port DNS servers.
func parseResolvers(raw string) ([]string, error) {
	if raw == "" {
		return nil, nil
	}
	var out []string
	for item := range strings.SplitSeq(raw, ",") {
		item = strings.TrimSpace(item)
		host, port, err := net.SplitHostPort(item)
		n, perr := strconv.Atoi(port)
		if err != nil || perr != nil || n < 1 || n > 65535 || (!isIPHost(host) && !validHost(normaliseHost(host))) {
			return nil, fmt.Errorf("%w: WARDLOCK_DNS_RESOLVERS must be comma-separated host:port pairs, such as 9.9.9.9:53,[2620:fe::fe]:53; %q is not one", ErrInvalidConfig, item)
		}
		out = append(out, net.JoinHostPort(host, port))
	}
	return out, nil
}

// positiveInt parses s as an integer of at least 1, or returns fallback
// for "".
func positiveInt(s string, fallback int64) (int64, error) {
	if s == "" {
		return fallback, nil
	}
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0, err
	}
	if v < 1 {
		return 0, errors.New("below 1")
	}
	return v, nil
}

// decodeSecret accepts base64url or standard base64, padded or not. The
// error never includes the value.
func decodeSecret(raw string) ([]byte, error) {
	raw = strings.TrimRight(raw, "=")
	key, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		key, err = base64.RawStdEncoding.DecodeString(raw)
	}
	if err != nil {
		return nil, fmt.Errorf("%w: WARDLOCK_SECRET is not base64url", ErrInvalidConfig)
	}
	if len(key) < MinSecretBytes {
		return nil, fmt.Errorf("%w: WARDLOCK_SECRET must decode to at least %d bytes, got %d", ErrInvalidConfig, MinSecretBytes, len(key))
	}
	return key, nil
}

func normaliseHost(h string) string {
	return strings.TrimSuffix(strings.ToLower(h), ".")
}

func validHost(h string) bool {
	if h == "" || len(h) > 253 {
		return false
	}
	for label := range strings.SplitSeq(h, ".") {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, r := range label {
			if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '-' {
				return false
			}
		}
	}
	return true
}

// TLS modes (WARDLOCK_TLS_MODE; docs/ARCHITECTURE.md "TLS modes").
const (
	// TLSLocal serves plain HTTP for localhost and *.localhost hosts.
	TLSLocal = "local"
	// TLSProxy serves plain HTTP behind a proxy that terminates TLS.
	TLSProxy = "proxy"
	// TLSAuto obtains certificates itself and serves HTTPS.
	TLSAuto = "auto"
)

// LetsEncrypt is the default ACME directory.
const LetsEncrypt = "https://acme-v02.api.letsencrypt.org/directory"

// TLSConfig is the TLS mode and the settings that belong to it.
type TLSConfig struct {
	// Mode is TLSLocal, TLSProxy or TLSAuto.
	Mode string
	// The remaining fields apply to TLSAuto only.
	HTTPSAddr string
	// ACMECA is the ACME directory URL; ACMEEmail the optional account
	// contact; ACMECARoot an optional PEM bundle trusted for the CA.
	ACMECA     string
	ACMEEmail  string
	ACMECARoot string
	// DNSProvider names the libdns provider for DNS-01; DNSAPIToken is
	// its credential.
	DNSProvider string
	DNSAPIToken Secret
	// TrustedProxies (TLSProxy only) are the peers whose X-Forwarded-*
	// headers are believed.
	TrustedProxies []netip.Prefix
}

// autoOnly are the variables that only automatic TLS reads; setting one in
// another mode is almost certainly a missing WARDLOCK_TLS_MODE=auto.
var autoOnly = []string{"WARDLOCK_ACME_CA", "WARDLOCK_ACME_EMAIL", "WARDLOCK_ACME_CA_ROOT", "WARDLOCK_DNS_PROVIDER", "WARDLOCK_DNS_API_TOKEN"}

func parseTLSConfig(get func(key, fallback string) string, base, dashboard string) (TLSConfig, error) {
	local := isLocalHost(base) && isLocalHost(dashboard)
	mode := TLSProxy
	if local {
		mode = TLSLocal
	}
	t := TLSConfig{Mode: get("WARDLOCK_TLS_MODE", mode)}
	switch t.Mode {
	case TLSLocal:
		if !local {
			return TLSConfig{}, fmt.Errorf("%w: WARDLOCK_TLS_MODE=local needs localhost or *.localhost base domain and dashboard host; use proxy or auto for %q", ErrInvalidConfig, base)
		}
	case TLSProxy, TLSAuto:
		if t.Mode == TLSAuto && (isLocalHost(base) || isLocalHost(dashboard) || isIPHost(base) || isIPHost(dashboard)) {
			return TLSConfig{}, fmt.Errorf("%w: WARDLOCK_TLS_MODE=auto needs public DNS names for WARDLOCK_BASE_DOMAIN and WARDLOCK_DASHBOARD_HOST, not localhost or IP addresses", ErrInvalidConfig)
		}
	default:
		return TLSConfig{}, fmt.Errorf("%w: WARDLOCK_TLS_MODE must be local, proxy or auto", ErrInvalidConfig)
	}

	if raw := get("WARDLOCK_TRUSTED_PROXIES", ""); raw != "" {
		if t.Mode != TLSProxy {
			return TLSConfig{}, fmt.Errorf("%w: WARDLOCK_TRUSTED_PROXIES is only used when WARDLOCK_TLS_MODE=proxy (the mode is %s)", ErrInvalidConfig, t.Mode)
		}
		proxies, err := parseTrustedProxies(raw)
		if err != nil {
			return TLSConfig{}, err
		}
		t.TrustedProxies = proxies
	}
	if t.Mode != TLSAuto {
		for _, key := range autoOnly {
			if get(key, "") != "" {
				return TLSConfig{}, fmt.Errorf("%w: %s is only used when WARDLOCK_TLS_MODE=auto (the mode is %s)", ErrInvalidConfig, key, t.Mode)
			}
		}
		return t, nil
	}

	t.HTTPSAddr = get("WARDLOCK_HTTPS_ADDR", ":443")
	if _, _, err := net.SplitHostPort(t.HTTPSAddr); err != nil {
		return TLSConfig{}, fmt.Errorf("%w: WARDLOCK_HTTPS_ADDR %q: %w", ErrInvalidConfig, t.HTTPSAddr, err)
	}
	t.ACMECA = get("WARDLOCK_ACME_CA", LetsEncrypt)
	if u, err := url.Parse(t.ACMECA); err != nil || u.Scheme != "https" || u.Host == "" {
		return TLSConfig{}, fmt.Errorf("%w: WARDLOCK_ACME_CA must be an https:// ACME directory URL", ErrInvalidConfig)
	}
	t.ACMEEmail = get("WARDLOCK_ACME_EMAIL", "")
	if t.ACMEEmail != "" {
		if _, err := mail.ParseAddress(t.ACMEEmail); err != nil {
			return TLSConfig{}, fmt.Errorf("%w: WARDLOCK_ACME_EMAIL is not an email address", ErrInvalidConfig)
		}
	}
	t.ACMECARoot = get("WARDLOCK_ACME_CA_ROOT", "")
	t.DNSProvider = strings.ToLower(get("WARDLOCK_DNS_PROVIDER", ""))
	if t.DNSProvider == "" {
		return TLSConfig{}, fmt.Errorf("%w: WARDLOCK_DNS_PROVIDER is required when WARDLOCK_TLS_MODE=auto: the wildcard certificate for *.%s needs the DNS-01 challenge", ErrInvalidConfig, base)
	}
	t.DNSAPIToken = Secret(get("WARDLOCK_DNS_API_TOKEN", ""))
	if t.DNSAPIToken == "" {
		return TLSConfig{}, fmt.Errorf("%w: WARDLOCK_DNS_API_TOKEN is required when WARDLOCK_TLS_MODE=auto", ErrInvalidConfig)
	}
	return t, nil
}

// parseTrustedProxies reads comma-separated IP addresses and CIDR ranges.
// IPv4-mapped IPv6 forms become plain IPv4. A range covering every address
// (0.0.0.0/0, ::/0) is refused: it would let any client choose its own
// address.
func parseTrustedProxies(raw string) ([]netip.Prefix, error) {
	var out []netip.Prefix
	for item := range strings.SplitSeq(raw, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		p, err := netip.ParsePrefix(item)
		if err != nil {
			addr, aerr := netip.ParseAddr(item)
			if aerr != nil {
				return nil, fmt.Errorf("%w: WARDLOCK_TRUSTED_PROXIES: %q is not an IP address or CIDR range", ErrInvalidConfig, item)
			}
			if addr.Zone() != "" {
				return nil, fmt.Errorf("%w: WARDLOCK_TRUSTED_PROXIES: %q has an IPv6 zone; list the address without it", ErrInvalidConfig, item)
			}
			p = netip.PrefixFrom(addr, addr.BitLen())
		}
		if p.Addr().Is4In6() {
			if p.Bits() < 96 {
				return nil, fmt.Errorf("%w: WARDLOCK_TRUSTED_PROXIES: %q mixes IPv4-mapped and IPv6 addresses; list the IPv4 range instead", ErrInvalidConfig, item)
			}
			p = netip.PrefixFrom(p.Addr().Unmap(), p.Bits()-96)
		}
		if p.Bits() == 0 {
			return nil, fmt.Errorf("%w: WARDLOCK_TRUSTED_PROXIES: %q trusts every address, which lets any client choose its own; list only the proxy's addresses", ErrInvalidConfig, item)
		}
		out = append(out, p.Masked())
	}
	return out, nil
}

func isLocalHost(h string) bool {
	return h == "localhost" || strings.HasSuffix(h, ".localhost")
}

func isIPHost(h string) bool {
	_, err := netip.ParseAddr(h)
	return err == nil
}
