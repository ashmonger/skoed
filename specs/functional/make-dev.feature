Feature: make dev — Vite hot-reload for the SPA
  As a frontend-leaning operator iterating on the skoed SPA
  I want one command that runs the Go daemon AND vite dev together
  And forwards API calls from vite to the running daemon
  So I can edit a .vue file and see it reload instantly without
  rebuilding the Go binary.

  Background:
    Given the repository is checked out
    And node + npm are installed on PATH
    And the skoed binary builds successfully

  @fsid:FS-MakeDevStartsBothProcesses
  Scenario: make dev launches skoed + vite together and proxies API calls
    When the operator runs `make dev` from the repo root
    Then a skoed daemon is started on port 18099
    And `vite dev` is started on port 5173
    And `curl http://localhost:5173/` returns 200 (the SPA shell from Vite)
    And `curl http://localhost:5173/api/v1/health` returns 200
      (the request is proxied to the daemon on 18099)
    And `curl http://localhost:5173/metrics` returns 200
      (Prometheus exporter proxied the same way)
    And the daemon writes logs under ~/tmp/skoed-dev/skoed.log

  @fsid:FS-MakeDevHmrOnVueEdit
  Scenario: editing a .vue file triggers HMR in the browser
    Given `make dev` is running with a browser pointed at http://localhost:5173/
    When the operator edits a .vue file under web/src/
    Then vite emits an HMR update for that module
    And the browser receives the update without a full reload
    And the Go binary is NOT rebuilt

  @fsid:FS-MakeDevCleanCtrlCShutdown
  Scenario: Ctrl-C stops both processes cleanly with no zombies
    Given `make dev` is running
    When the operator sends SIGINT (Ctrl-C) to the make process
    Then the skoed daemon exits with status 0 (or signal-terminated)
    And the vite dev server exits
    And no orphan skoed or node processes remain on the host
    And subsequent `make dev` runs find both ports free

  Non-goals:
    - Replacing the production embedded-binary model (this is dev-only)
    - Auto-rebuilding the Go binary on *.go change (use air/entr if needed)
    - Multi-node cluster dev mode (see M5.9.2 stretch goal — out of scope here)
    - Hot-reload for embedded static assets served by the Go binary
      (only the Vite dev server serves the SPA in this mode)
