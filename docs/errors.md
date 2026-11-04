# Protocol Error Codes

Keel answers a client below its protocol floor with a stable code, `KEEL-PROTO-NNN`. A code never changes meaning;
a new reason gets a new code. Where keel can still speak HTTP to the client, it answers with `301 Moved Permanently`
to the notice page, and the response body is an RFC 9457 problem (`application/problem+json`). Where no HTTP
channel exists (the TLS handshake fails), keel logs the code (`tls_handshake_rejected`) and counts it
(`keel_tls_handshake_rejections_total{code}`), so an operator can hand the code to the client's owner.

Policy and configuration: [security.md §8](security.md#8-protocol-rejections).

## Response

```http
HTTP/1.1 301 Moved Permanently
Location: http://api.example:8080/.well-known/keel/upgrade?code=KEEL-PROTO-007
Cache-Control: no-store
Connection: close
Content-Type: application/problem+json
Keel-Error-Code: KEEL-PROTO-007

{"type":"https://github.com/keelcore/keel/blob/main/docs/errors.md#keel-proto-007","title":"Newer HTTP version required","status":301,"detail":"This service requires a newer HTTP version. Upgrade your client.","code":"KEEL-PROTO-007","reason":"http_version_below_minimum","required":["HTTP/2"],"notice_url":"http://api.example:8080/.well-known/keel/upgrade?code=KEEL-PROTO-007","request_id":"01JD…"}
```

| Member | Meaning |
| --- | --- |
| `type` | This page's anchor for the code (RFC 9457) |
| `title`, `detail` | What is required, for a person |
| `status` | `301`, or `426` when no notice page is reachable (the problem is then answered in place) |
| `code`, `reason` | The stable code and its reason |
| `required` | What the client must support |
| `notice_url` | The human-readable notice; browsers follow `Location` to it |
| `request_id` | The request's ID in keel's logs, when the request reached HTTP |

The notice page, `GET /.well-known/keel/upgrade?code=<code>` on the plain-HTTP listener, serves HTML to browsers,
the problem JSON for `Accept: application/json` (or `?format=json`), and plain text otherwise, so `curl -L` prints
it in the terminal.

## Codes

### KEEL-PROTO-001

`tls_version_below_1_3` — the client's highest TLS version is below 1.3. Required: TLS 1.3. The handshake fails;
logged and counted.

### KEEL-PROTO-002

`no_shared_alpn` — the client offers only application protocols keel does not serve. Required: `h2` or
`http/1.1`. The handshake fails; logged and counted.

### KEEL-PROTO-003

`no_shared_group` — no key exchange group in common (a FIPS build accepts P-256 and P-384). The handshake fails;
logged and counted.

### KEEL-PROTO-004

`no_shared_cipher` — no TLS 1.3 cipher suite in common (a FIPS build accepts the AES-GCM suites). The handshake
fails; logged and counted.

### KEEL-PROTO-005

`client_certificate_required` — inbound mTLS (`tls.client_ca_file`) and the client presented no certificate. With
`tls.client_cert_notice: true` the request gets the 301; otherwise the handshake fails.

### KEEL-PROTO-006

`plaintext_on_https_port` — plain HTTP sent to the HTTPS port. Required: `https`. Answered with the 301.

### KEEL-PROTO-007

`http_version_below_minimum` — the request's HTTP version is below `security.min_http_version`. Required: `HTTP/2`
or `HTTP/3`. Answered with the 301; for HTTP/3 the response also carries `Alt-Svc`.
