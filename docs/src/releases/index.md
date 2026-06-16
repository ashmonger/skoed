# Releases & test reports

Real-environment test reports are published for each milestone that includes
a Proxmox cluster validation run.

| Release | Feature | Test report |
|---------|---------|-------------|
| — (pending) | M15 — Test suite cleanup + keepalived VIP | [Test report](m15-test-report.html) |
| [v0.1.2](https://github.com/ashmonger/skoed/releases/tag/v0.1.2) | M14 — Block dynamic-lease clients | [Test report](m14-test-report.html) |
| [v0.1.1](https://github.com/ashmonger/skoed/releases/tag/v0.1.1) | M13 — Filtering pause | — |
| [v0.1.0](https://github.com/ashmonger/skoed/releases/tag/v0.1.0) | M11 — Distribution & packaging | — |

## M15 — Test suite cleanup + keepalived VIP

409/409 acceptance tests pass. Real-environment keepalived VIP deployment and failover
test on a 3-node Proxmox LXC cluster (CT301/302/303, Raft quorum):

- **Alt-Svc header**: DoH/HTTP2 responses now include `Alt-Svc: h3=":port"; ma=86400`; browsers auto-upgrade to HTTP/3
- **Test timing**: 19 previously flaking tests (6 `TestAutoRefresh*` + 13 `TestDohResolverDb*`) now pass reliably with `SKOED_TEST_MODE=1`
- **keepalived VIP failover**: VIP `10.0.0.10` fails over in <14 s; health check passes throughout
- **5 provisioning bugs fixed**: Raft snapshot replication, Basic Auth removal, wrong health field, missing curl, wrong api_address

[→ Full test report](m15-test-report.html)

## M14 — Block dynamic-lease clients

10/10 acceptance tests pass. Real-environment validation on a 3-node Proxmox LXC
cluster (CT301/302/303, Raft quorum, dnsmasq DHCP on CT301):

- **DNS filtering**: 15/15 tests pass with `SKOED_TEST_MODE=1` + EDNS0 option 65500
- **DNS load (forwarding)**: 14,583 QPS peak, 0% loss
- **DNS load (recursive)**: 5,998 QPS peak, 0.10% loss
- **API load**: 391 req/s, p95 1.94 ms, 0% errors (k6 20 VUs × 20s)

[→ Full test report](m14-test-report.html)
