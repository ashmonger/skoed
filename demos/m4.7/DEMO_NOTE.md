# DEMO NOTE — M4.7 DNS Cache Controls

## Scope

Exposes DNS cache statistics and purge controls via the management API. Previously the cache was a black box; this milestone surfaces hit/miss/eviction counters and lets operators flush it on demand.

### Implemented

- `GET /api/v1/dns/cache/stats` — returns size, max_entries, hits, misses, evictions
- `POST /api/v1/dns/cache/purge` — flushes the entire in-memory DNS cache
- Cache stats wired into Prometheus metrics (`skoed_dns_cache_*` series) in M5.1

### Not implemented

- Per-domain cache eviction (purge a single domain)
- Cache pre-warming

## Demo

```bash
# Get cache stats
curl -u admin:pass http://localhost:8080/api/v1/dns/cache/stats

# Purge cache
curl -u admin:pass -X POST http://localhost:8080/api/v1/dns/cache/purge
```

## Limitations

Purge is cluster-local — it flushes only the node it's called on. Purge each follower separately for cluster-wide flush.
