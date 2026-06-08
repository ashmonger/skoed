package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Credentials are read from (priority order):
//   1. --auth user:pass
//   2. SKOED_AUTH env var
//   3. ~/.skoed/credentials
//   4. (interactive prompt — out of scope for v1)
//
// File format is YAML-ish but kept JSON-decodable so we don't pull
// another dep:
//   {"api_url":"http://127.0.0.1:8080","username":"admin","password":"…"}
//
// Mode is enforced 0600 — refuses to read world-readable creds.

type Credentials struct {
	APIURL   string `json:"api_url,omitempty"`
	Username string `json:"username"`
	Password string `json:"password"`
}

// LoadCredentials resolves credentials from the priority chain. The
// authFlag and apiFlag are the --auth and --api command-line values
// (may be empty).
func LoadCredentials(authFlag, apiFlag string) (Credentials, error) {
	c := Credentials{}
	// 1. --auth flag
	if u, p, ok := splitAuth(authFlag); ok {
		c.Username, c.Password = u, p
	}
	// 2. env
	if c.Username == "" {
		if v := os.Getenv("SKOED_AUTH"); v != "" {
			if u, p, ok := splitAuth(v); ok {
				c.Username, c.Password = u, p
			}
		}
	}
	// 3. file
	if c.Username == "" {
		fileC, err := loadCredentialsFile()
		if err != nil {
			return c, err
		}
		if fileC != nil {
			c = *fileC
		}
	}
	// API URL overrides file value.
	if apiFlag != "" {
		c.APIURL = apiFlag
	} else if c.APIURL == "" {
		if v := os.Getenv("SKOED_API"); v != "" {
			c.APIURL = v
		} else {
			c.APIURL = "http://127.0.0.1:8080"
		}
	}
	return c, nil
}

func splitAuth(s string) (user, pass string, ok bool) {
	if s == "" {
		return "", "", false
	}
	i := strings.IndexByte(s, ':')
	if i <= 0 || i == len(s)-1 {
		return "", "", false
	}
	return s[:i], s[i+1:], true
}

func loadCredentialsFile() (*Credentials, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, nil
	}
	path := filepath.Join(home, ".skoed", "credentials")
	fi, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	if fi.Mode().Perm()&0o077 != 0 {
		return nil, fmt.Errorf("refusing to read world/group-readable credentials at %s (mode %o); chmod 600", path, fi.Mode().Perm())
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var c Credentials
	if err := json.Unmarshal(b, &c); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return &c, nil
}
