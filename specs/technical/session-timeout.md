# Session Timeout — Technical Specification

x-tsid: TS-SessionTimeout  
x-fsid-links: [FS-SessionTimeoutViewCurrentSetting, FS-SessionTimeoutSetCustomDuration, FS-SessionTimeoutDefaultApplied, FS-SessionTimeoutExpiredSessionRedirectedToLogin, FS-SessionTimeoutExistingSessionsUnaffected, FS-SessionTimeoutPersistsAcrossRestart]

## Overview

Session timeout is a cluster-wide setting that controls how long a web UI session (Bearer token) remains valid after login. It is stored in the Raft-replicated configuration and read at login time to set the token's expiry.

## Storage

`session_timeout_seconds` lives in the `AuthConfig` struct in `internal/config/config.go`:

```go
type AuthConfig struct {
    Username              string `yaml:"username"`
    PasswordHash          string `yaml:"password_hash"`
    SessionTimeoutSeconds int    `yaml:"session_timeout_seconds,omitempty" json:"session_timeout_seconds,omitempty"`
}
```

It is persisted in the bbolt `config_auth` bucket via the `CmdAuthSetCredentials` Raft command. The `omitempty` tag means a zero value (never set) is omitted from serialized forms; all readers apply a default-for-zero of 28800 seconds.

## Raft replication

`PATCH /api/v1/settings` with `auth.session_timeout_seconds` calls `cluster.SetCredentials(username, passwordHash, sessionTimeoutSeconds)` which applies a `CmdAuthSetCredentials` Raft log entry. The entry is replicated to all nodes before the response is returned to the client.

The `importM1Config` path in `internal/cluster/store.go` reads `AuthConfig.SessionTimeoutSeconds` from the imported config and includes it in the `CmdAuthSetCredentials` payload, ensuring it survives config import and cluster restores.

## Session creation

`internal/api/app.go — App.CreateSession`:

```go
func (a *App) CreateSession(rawToken, username string) {
    ttl := sessionTTLFromSeconds(a.GetCfg().Auth.SessionTimeoutSeconds)
    a.sessions.create(rawToken, username, ttl)
}
```

The TTL is sampled from the live config at the moment of login. Sessions created before a setting change retain their original TTL.

`sessionTTLFromSeconds` in `internal/api/session.go`:

```go
const defaultSessionTTL = 8 * time.Hour

func sessionTTLFromSeconds(s int) time.Duration {
    if s <= 0 {
        return defaultSessionTTL
    }
    return time.Duration(s) * time.Second
}
```

## Session store

Sessions are stored in an in-memory map per node (`internal/api/session.go`). Each entry carries an expiry timestamp. On every authenticated request the middleware checks `time.Now().After(expiry)` and returns HTTP 401 if the session has expired. The session store is node-local — a token issued on node A is not valid on node B without a separate login.

## API contract

See `specs/technical/management-api.openapi.yaml` — `Settings` schema, `auth.session_timeout_seconds` property and `SettingsPatch` schema.

**GET /api/v1/settings** — returns `auth.session_timeout_seconds` (defaulting to 28800 when the stored value is 0).

**PATCH /api/v1/settings** — accepts `{"auth": {"session_timeout_seconds": N}}`:
- `N` must be in the range [1, 604800].
- Values outside the range return HTTP 400 with `{"error": "auth.session_timeout_seconds must be between 1 and 604800"}`.
- The change is applied cluster-wide via Raft before the response is returned.

## Restart persistence

Session timeout is stored in the Raft-replicated bbolt state, so it survives node restarts. The effective restart behaviour depends on `SnapshotThreshold`:

- With `SnapshotThreshold=64` (M34.5 default), restarts replay at most 64 log entries (<1 s). The configured timeout is available immediately after the node comes back online.
- The in-memory session store is not persisted. All active sessions are lost on restart; administrators must re-login.

## Web UI

`web/src/views/Settings.vue` — Auth section contains a `<select>` with the following options:

| Label    | Value (seconds) |
|----------|-----------------|
| 30 minutes | 1800          |
| 1 hour   | 3600            |
| 4 hours  | 14400           |
| 8 hours (default) | 28800  |
| 24 hours | 86400           |
| 7 days   | 604800          |

Selecting an option and clicking Save triggers `PATCH /api/v1/settings`. The current value is loaded from `GET /api/v1/settings` on page mount.

## Validation

| Constraint | Value |
|-----------|-------|
| Minimum | 1 s |
| Maximum | 604800 s (7 days) |
| Default | 28800 s (8 hours) |
| Validation error | HTTP 400 |
| Replication | Raft (all nodes updated before response) |
