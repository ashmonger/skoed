# Set the admin password

skoed ships with NO admin credentials. The first request to the
management API must be a `POST /api/v1/auth/setup` that creates
them.

```sh
# Replace <HOST> with the IP / hostname of your skoed node.
curl -fsS -X POST http://<HOST>:8080/api/v1/auth/setup \
  -H 'content-type: application/json' \
  -d '{"username":"admin","password":"<your-password>"}'
```

The response is HTTP 201 on first call. Subsequent calls return 409
("auth already configured"). Use [change password](../configuration/api-https.md#change-password)
afterwards if needed.

## Open the Web UI

Browse to `http://<HOST>:8080` and log in as `admin`.

You'll land on the Dashboard:

- Cluster status, mode, members, total queries (windowed)
- Query breakdown (blocked / forwarded / cached / local)
- Top blocked domains
- Cluster nodes table

Any of these cards may be empty until a few DNS queries run through
the resolver — point a client at the skoed IP, browse a couple of
sites, refresh.

## Point a client at skoed

The simplest test: a one-shot `dig`:

```sh
dig @<HOST> example.com
```

Real clients (router DHCP option 6, OS network settings) need the
skoed IP set as their resolver. skoed listens on UDP/TCP 53 by
default; if you want DoH or DoT serving instead, see
[DoH / DoT serving](../configuration/doh-dot.md).

## Next

- [Add your first blocklist](first-blocklist.md)
- [Bootstrap a 3-node cluster](../cluster/bootstrap.md)
