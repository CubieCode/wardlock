// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"fmt"
	"os"

	"github.com/minio/minio-go/v7/pkg/credentials"

	"wardlock.nz/server/internal/deploys"
	"wardlock.nz/server/internal/platform"
)

// blobStore is the configured blob backend: what deploys and the edge
// read and write, and what GC sweeps.
type blobStore interface {
	deploys.BlobStore
	deploys.BlobFiles
}

// openBlobs returns the configured blob store and its readiness check:
// the directory is writable, or the bucket is reachable. It creates the
// blob directory, so readiness passes from the start rather than from the
// first upload; it makes no request to a bucket.
func openBlobs(cfg platform.Config) (blobStore, platform.Check, error) {
	if cfg.BlobBackend != platform.BlobS3 {
		if err := os.MkdirAll(cfg.BlobDir, 0o700); err != nil {
			return nil, platform.Check{}, fmt.Errorf("create blob directory: %w", err)
		}
		return deploys.NewFSBlobStore(cfg.BlobDir), platform.Check{Name: "blobs", Run: platform.DirWritable(cfg.BlobDir)}, nil
	}
	s := cfg.S3
	bs, err := deploys.NewS3BlobStore(deploys.S3Config{
		Endpoint:      s.Endpoint,
		Bucket:        s.Bucket,
		Region:        s.Region,
		Prefix:        s.Prefix,
		PathStyle:     s.Addressing == platform.S3AddressPath,
		VirtualHosted: s.Addressing == platform.S3AddressVirtual,
		Creds:         s3Credentials(s),
		AppName:       "wardlock",
		AppVersion:    platform.Version,
	})
	if err != nil {
		return nil, platform.Check{}, err
	}
	return bs, platform.Check{Name: "blobs", Run: bs.Check}, nil
}

// s3Credentials follows the AWS order: keys from the environment, then
// the shared credentials and config files (including SSO profiles), then
// a container, web identity or EC2 instance role.
func s3Credentials(s platform.S3Config) *credentials.Credentials {
	if s.AccessKeyID != "" {
		return credentials.NewStaticV4(s.AccessKeyID, string(s.SecretAccessKey), string(s.SessionToken))
	}
	return credentials.NewChainCredentials([]credentials.Provider{
		&credentials.FileAWSCredentials{Filename: s.CredentialsFile, Profile: s.Profile},
		&credentials.IAM{},
	})
}
