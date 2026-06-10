# API Reference (OpenAPI)

skoed's management REST API is fully described by an OpenAPI 3.0 specification. The spec is served directly by the running daemon, so it always matches the version you have deployed.

## Interactive documentation (Swagger UI)

skoed embeds a [Swagger UI](https://swagger.io/tools/swagger-ui/) at:

```
http://<host>:8080/api/docs
```

Open this URL in a browser while skoed is running to explore all endpoints, read parameter descriptions, and send requests directly from the browser.

### Authentication in the Swagger UI

The Swagger UI supports both authentication methods that skoed accepts:

- **Bearer token** — click the **Authorize** button (lock icon), select `bearerAuth`, and enter your API token. All subsequent requests from the UI will include `Authorization: Bearer <token>`.
- **Basic auth** — click **Authorize**, select `basicAuth`, and enter your username and password.

You can revoke authorization at any time by clicking **Authorize** again and selecting **Logout**.

## Raw OpenAPI spec

The machine-readable OpenAPI 3.0 YAML spec is available at:

```
http://<host>:8080/api/openapi.yaml
```

### Download the spec

```bash
curl http://localhost:8080/api/openapi.yaml > openapi.yaml
```

Use the downloaded spec to:

- Generate client SDKs with [openapi-generator](https://github.com/OpenAPITools/openapi-generator) or [oapi-codegen](https://github.com/oapi-codegen/oapi-codegen).
- Import into Postman or Insomnia.
- Run contract tests against the API.

## Kubernetes: port-forward to access the UI

When skoed is deployed in Kubernetes and the service is not exposed externally, use `kubectl port-forward` to reach the UI from your local machine:

```bash
kubectl port-forward svc/skoed 8080:8080
```

Then open [http://localhost:8080/api/docs](http://localhost:8080/api/docs) in your browser. The port-forward remains active until you press `Ctrl-C`.

If your skoed pods run in a non-default namespace:

```bash
kubectl port-forward -n <namespace> svc/skoed 8080:8080
```
