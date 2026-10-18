// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: MIT

package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// Stored credentials (docs/SPEC.md "Owners and sign-in"): the token from
// `wardlock login` lives in the OS keychain under keyringService, keyed by
// the server URL, and credentials.json in the config directory records which
// server that is. Where no keychain is available the token goes into
// credentials.json too, which is always written with mode 0600.
const (
	keyringService  = "wardlock"
	credentialsFile = "credentials.json"
)

var (
	errNotLoggedIn    = errors.New("not logged in")
	errSecretNotFound = errors.New("secret not found")
)

// secretStore is the OS keychain, replaceable in tests.
type secretStore interface {
	Get(service, user string) (string, error)
	Set(service, user, secret string) error
	// Delete answers errSecretNotFound when there is nothing to delete.
	Delete(service, user string) error
}

type credentialStore struct {
	keyring secretStore
	// dir returns the config directory (…/wardlock).
	dir func() (string, error)
}

// storedCredentials is credentials.json.
type storedCredentials struct {
	Server string `json:"server"`
	// Token is set only when the keychain was unavailable.
	Token string `json:"token,omitempty"`
}

// configDir is $XDG_CONFIG_HOME/wardlock, else ~/.config/wardlock on every
// platform, as docs/SPEC.md names it.
func configDir(getenv func(string) string) (string, error) {
	if x := getenv("XDG_CONFIG_HOME"); x != "" && filepath.IsAbs(x) {
		return filepath.Join(x, "wardlock"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("find the config directory: %w", err)
	}
	return filepath.Join(home, ".config", "wardlock"), nil
}

// storage says where a token was saved: "keychain", or the file's path.
type storage string

const keychainStorage storage = "keychain"

// save stores token for server, replacing any earlier login.
func (c credentialStore) save(server, token string) (storage, error) {
	if old, err := c.read(); err == nil && old.Server != server {
		_ = c.keyring.Delete(keyringService, old.Server)
	}
	where := keychainStorage
	rec := storedCredentials{Server: server}
	if err := c.keyring.Set(keyringService, server, token); err != nil {
		rec.Token = token
		where = ""
	}
	path, err := c.write(rec)
	if err != nil {
		if where == keychainStorage {
			_ = c.keyring.Delete(keyringService, server)
		}
		return "", err
	}
	if where == "" {
		where = storage(path)
	}
	return where, nil
}

// load returns the stored server and token, or errNotLoggedIn.
func (c credentialStore) load() (server, token string, err error) {
	rec, err := c.read()
	if err != nil {
		return "", "", err
	}
	if rec.Token != "" {
		return rec.Server, rec.Token, nil
	}
	token, err = c.keyring.Get(keyringService, rec.Server)
	if err != nil {
		return rec.Server, "", fmt.Errorf("read the token for %s from the keychain: %w", rec.Server, err)
	}
	return rec.Server, token, nil
}

// forget removes the stored token and file. Missing ones are not errors.
// When rec says the token lives in the file, the keychain is only tidied:
// it may not exist on this computer at all.
func (c credentialStore) forget(rec storedCredentials) error {
	if err := c.keyring.Delete(keyringService, rec.Server); err != nil && !errors.Is(err, errSecretNotFound) && rec.Token == "" {
		return fmt.Errorf("remove the token from the keychain: %w", err)
	}
	dir, err := c.dir()
	if err != nil {
		return err
	}
	if err := os.Remove(filepath.Join(dir, credentialsFile)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("remove credentials: %w", err)
	}
	return nil
}

func (c credentialStore) read() (storedCredentials, error) {
	dir, err := c.dir()
	if err != nil {
		return storedCredentials{}, err
	}
	b, err := readInDir(dir, credentialsFile)
	if errors.Is(err, fs.ErrNotExist) {
		return storedCredentials{}, errNotLoggedIn
	}
	if err != nil {
		return storedCredentials{}, fmt.Errorf("read credentials: %w", err)
	}
	var rec storedCredentials
	if err := json.Unmarshal(b, &rec); err != nil || rec.Server == "" {
		return storedCredentials{}, fmt.Errorf("%s is not valid; run wardlock login again", filepath.Join(dir, credentialsFile))
	}
	return rec, nil
}

// makePrivateDir sets dir to 0700 (owner only; a directory needs the
// execute bit to be entered).
func makePrivateDir(dir string) error {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()
	return root.Chmod(".", 0o700)
}

// readInDir reads name inside dir and nowhere else.
func readInDir(dir, name string) ([]byte, error) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	defer func() { _ = root.Close() }()
	return root.ReadFile(name)
}

// write replaces credentials.json atomically. The directory is 0700 and
// the file 0600 from the moment it exists, so the token is never readable
// by other users, even briefly.
func (c credentialStore) write(rec storedCredentials) (string, error) {
	dir, err := c.dir()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("create %s: %w", dir, err)
	}
	// MkdirAll leaves an existing directory as it was; tighten it.
	if err := makePrivateDir(dir); err != nil {
		return "", fmt.Errorf("make %s private: %w", dir, err)
	}
	b, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return "", err
	}
	tmp, err := os.CreateTemp(dir, ".credentials-*.json") // created 0600
	if err != nil {
		return "", fmt.Errorf("write credentials: %w", err)
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return "", fmt.Errorf("write credentials: %w", err)
	}
	if _, err := tmp.Write(append(b, '\n')); err != nil {
		_ = tmp.Close()
		return "", fmt.Errorf("write credentials: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return "", fmt.Errorf("write credentials: %w", err)
	}
	path := filepath.Join(dir, credentialsFile)
	if err := os.Rename(tmp.Name(), path); err != nil {
		return "", fmt.Errorf("write credentials: %w", err)
	}
	return path, nil
}

// credential is the token a command should use and the server it is for.
type credential struct {
	server, token string
	// fromEnv is true when WARDLOCK_TOKEN supplied the token.
	fromEnv bool
}

// resolveCredential picks the token for a command: WARDLOCK_TOKEN when set
// (for CI), else the one stored by `wardlock login`. The server is
// serverFlag, else $WARDLOCK_SERVER, else the server saved at login.
func (a *app) resolveCredential(serverFlag string) (credential, error) {
	server := serverFlag
	if server == "" {
		server = a.getenv("WARDLOCK_SERVER")
	}
	if tok := a.getenv("WARDLOCK_TOKEN"); tok != "" {
		if server == "" {
			if rec, err := a.creds.read(); err == nil {
				server = rec.Server
			}
		}
		if server == "" {
			return credential{}, fmt.Errorf("WARDLOCK_TOKEN is set but no server is: pass --server or set WARDLOCK_SERVER: %w", errUsage)
		}
		return credential{server: server, token: tok, fromEnv: true}, nil
	}
	stored, tok, err := a.creds.load()
	if errors.Is(err, errNotLoggedIn) {
		return credential{}, errors.New("not logged in: run wardlock login --server URL, or set WARDLOCK_TOKEN")
	}
	if err != nil {
		return credential{}, err
	}
	if server != "" && normaliseServer(server) != stored {
		return credential{}, fmt.Errorf("logged in to %s, not %s: run wardlock login --server %s", stored, server, server)
	}
	return credential{server: stored, token: tok}, nil
}
