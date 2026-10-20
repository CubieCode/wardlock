// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package platform

import (
	"fmt"
	"log/slog"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
)

// Blob backends (WARDLOCK_BLOB_BACKEND).
const (
	// BlobFS keeps blobs in WARDLOCK_BLOB_DIR.
	BlobFS = "fs"
	// BlobS3 keeps blobs in an S3-compatible bucket.
	BlobS3 = "s3"
)

// MaxS3FileSize is the largest WARDLOCK_MAX_FILE_SIZE the S3 backend
// accepts: one PutObject or CopyObject holds at most 5 GiB.
const MaxS3FileSize = 5 << 30

// S3 addressing styles (WARDLOCK_S3_PATH_STYLE).
const (
	// S3AddressAuto lets the client choose: virtual-hosted for AWS, path
	// style otherwise.
	S3AddressAuto = "auto"
	// S3AddressPath puts the bucket in the path: https://endpoint/bucket/key.
	S3AddressPath = "path"
	// S3AddressVirtual puts it in the host: https://bucket.endpoint/key.
	S3AddressVirtual = "virtual"
)

// S3Config is the bucket for the S3 blob backend. Credentials come from
// the standard AWS variables; when AWS_ACCESS_KEY_ID is unset the server
// reads the shared credentials file and then the instance or container
// role.
type S3Config struct {
	// Endpoint is the service's base URL, without a path.
	Endpoint *url.URL
	Bucket   string
	Region   string
	// Prefix is prepended to every key: "" or a path ending in "/".
	Prefix string
	// Addressing is S3AddressAuto, S3AddressPath or S3AddressVirtual.
	Addressing string
	// AccessKeyID, SecretAccessKey and SessionToken are AWS_ACCESS_KEY_ID,
	// AWS_SECRET_ACCESS_KEY and AWS_SESSION_TOKEN.
	AccessKeyID     string
	SecretAccessKey Secret
	SessionToken    Secret
	// CredentialsFile and Profile are AWS_SHARED_CREDENTIALS_FILE and
	// AWS_PROFILE; empty means ~/.aws/credentials and "default".
	CredentialsFile string
	Profile         string
}

// LogValue logs where blobs go and which credentials are used, never
// the credentials themselves.
func (s S3Config) LogValue() slog.Value {
	source := "environment"
	if s.AccessKeyID == "" {
		source = "shared file or role"
	}
	endpoint := ""
	if s.Endpoint != nil {
		endpoint = s.Endpoint.String()
	}
	return slog.GroupValue(
		slog.String("endpoint", endpoint),
		slog.String("bucket", s.Bucket),
		slog.String("region", s.Region),
		slog.String("prefix", s.Prefix),
		slog.String("addressing", s.Addressing),
		slog.String("credentials", source),
	)
}

// s3Only are the variables only the S3 backend reads.
var s3Only = []string{"WARDLOCK_S3_ENDPOINT", "WARDLOCK_S3_BUCKET", "WARDLOCK_S3_REGION", "WARDLOCK_S3_PREFIX", "WARDLOCK_S3_PATH_STYLE"}

// loadBlobs reads WARDLOCK_BLOB_BACKEND and the blob directory or bucket.
// It runs after the deploy limits, which the S3 backend caps.
func (c *Config) loadBlobs(get func(key, fallback string) string) error {
	c.BlobBackend = strings.ToLower(get("WARDLOCK_BLOB_BACKEND", BlobFS))
	switch c.BlobBackend {
	case BlobFS:
		for _, key := range s3Only {
			if get(key, "") != "" {
				return fmt.Errorf("%w: %s is only used when WARDLOCK_BLOB_BACKEND=s3", ErrInvalidConfig, key)
			}
		}
		c.BlobDir = get("WARDLOCK_BLOB_DIR", filepath.Join(c.DataDir, "blobs"))
		return nil
	case BlobS3:
	default:
		return fmt.Errorf("%w: WARDLOCK_BLOB_BACKEND must be fs or s3", ErrInvalidConfig)
	}
	if get("WARDLOCK_BLOB_DIR", "") != "" {
		return fmt.Errorf("%w: WARDLOCK_BLOB_DIR is only used when WARDLOCK_BLOB_BACKEND=fs", ErrInvalidConfig)
	}
	if c.MaxFileSize > MaxS3FileSize {
		return fmt.Errorf("%w: WARDLOCK_MAX_FILE_SIZE may be at most %d (5 GiB) with WARDLOCK_BLOB_BACKEND=s3", ErrInvalidConfig, int64(MaxS3FileSize))
	}
	s, err := parseS3Config(get)
	if err != nil {
		return err
	}
	c.S3 = s
	return nil
}

func parseS3Config(get func(key, fallback string) string) (S3Config, error) {
	var s S3Config
	raw := get("WARDLOCK_S3_ENDPOINT", "")
	if raw == "" {
		return S3Config{}, fmt.Errorf("%w: WARDLOCK_S3_ENDPOINT is required when WARDLOCK_BLOB_BACKEND=s3, such as https://s3.eu-west-1.amazonaws.com", ErrInvalidConfig)
	}
	u, err := url.Parse(raw)
	// The error never repeats the value: a pasted URL may carry a key.
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil ||
		(u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" {
		return S3Config{}, fmt.Errorf("%w: WARDLOCK_S3_ENDPOINT must be an http(s) URL with only a scheme, host and optional port, such as https://s3.eu-west-1.amazonaws.com; put the bucket in WARDLOCK_S3_BUCKET and any key prefix in WARDLOCK_S3_PREFIX", ErrInvalidConfig)
	}
	s.Endpoint = &url.URL{Scheme: u.Scheme, Host: u.Host}

	s.Bucket = get("WARDLOCK_S3_BUCKET", "")
	if !validBucket(s.Bucket) {
		return S3Config{}, fmt.Errorf("%w: WARDLOCK_S3_BUCKET must be a bucket name: 3 to 63 lowercase letters, digits, dots and hyphens, starting and ending with a letter or digit", ErrInvalidConfig)
	}
	s.Region = get("WARDLOCK_S3_REGION", "")
	if s.Region == "" || strings.ContainsFunc(s.Region, func(r rune) bool {
		return (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '-' && r != '_'
	}) {
		return S3Config{}, fmt.Errorf("%w: WARDLOCK_S3_REGION is required when WARDLOCK_BLOB_BACKEND=s3: the bucket's region, such as eu-west-1 (AWS), auto (Cloudflare R2), us-east-1 (MinIO) or garage (Garage)", ErrInvalidConfig)
	}
	if s.Prefix, err = parseS3Prefix(get("WARDLOCK_S3_PREFIX", "")); err != nil {
		return S3Config{}, err
	}
	s.Addressing = S3AddressAuto
	if raw := get("WARDLOCK_S3_PATH_STYLE", ""); raw != "" {
		path, err := strconv.ParseBool(raw)
		if err != nil {
			return S3Config{}, fmt.Errorf("%w: WARDLOCK_S3_PATH_STYLE must be true or false", ErrInvalidConfig)
		}
		s.Addressing = S3AddressVirtual
		if path {
			s.Addressing = S3AddressPath
		}
	}

	s.AccessKeyID = get("AWS_ACCESS_KEY_ID", "")
	s.SecretAccessKey = Secret(get("AWS_SECRET_ACCESS_KEY", ""))
	s.SessionToken = Secret(get("AWS_SESSION_TOKEN", ""))
	if (s.AccessKeyID == "") != (s.SecretAccessKey == "") {
		return S3Config{}, fmt.Errorf("%w: set AWS_ACCESS_KEY_ID and AWS_SECRET_ACCESS_KEY together, or neither to use the shared credentials file or an instance role", ErrInvalidConfig)
	}
	if s.SessionToken != "" && s.AccessKeyID == "" {
		return S3Config{}, fmt.Errorf("%w: AWS_SESSION_TOKEN needs AWS_ACCESS_KEY_ID and AWS_SECRET_ACCESS_KEY", ErrInvalidConfig)
	}
	s.CredentialsFile = get("AWS_SHARED_CREDENTIALS_FILE", "")
	s.Profile = get("AWS_PROFILE", "")
	return s, nil
}

// parseS3Prefix normalises WARDLOCK_S3_PREFIX to "" or "a/b/".
func parseS3Prefix(raw string) (string, error) {
	p := strings.Trim(raw, "/")
	if p == "" {
		return "", nil
	}
	bad := len(p) > 512 || strings.ContainsFunc(p, func(r rune) bool { return r < 0x20 || r == 0x7f || r == '\\' })
	for seg := range strings.SplitSeq(p, "/") {
		bad = bad || seg == "" || seg == "." || seg == ".."
	}
	if bad {
		return "", fmt.Errorf("%w: WARDLOCK_S3_PREFIX must be slash-separated names, such as wardlock/production, without empty, . or .. segments", ErrInvalidConfig)
	}
	return p + "/", nil
}

// validBucket reports whether b is a valid S3 bucket name.
func validBucket(b string) bool {
	if len(b) < 3 || len(b) > 63 || strings.Contains(b, "..") {
		return false
	}
	for i := range len(b) {
		c := b[i]
		alnum := (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9')
		if !alnum && ((c != '.' && c != '-') || i == 0 || i == len(b)-1) {
			return false
		}
	}
	return true
}
