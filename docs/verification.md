# Verification · 2026-11-01

Verified locally with Go 1.26.10, PostgreSQL 17, Redis-compatible miniredis, Node 24, Next.js 16.4.3 and Chromium. All provider traffic used fixtures; no paid LLM requests were made.

| Check | Observed result |
|---|---|
| `TEST_DATABASE_URL=… go test -race ./...` | 29 tests/subtests passed; the opt-in browser-fixture test is skipped in the normal run |
| `go vet ./...` | Passed |
| `go build ./cmd/...` | Passed |
| `go mod verify` | All modules verified |
| `pnpm typecheck` | Passed |
| `pnpm build` | Optimized production build passed |
| Playwright production UI flow | 1 test passed, using newly started backend fixture and standalone Next server |
| Docker Compose config | Validated with `docker compose … config --quiet` |
| `govulncheck` v1.9.0 | No reachable vulnerabilities found; three advisories in required modules were reported as not called by this code |
| `pnpm audit --prod` | No known vulnerabilities found |

## Tested behavior

- OpenAI and Anthropic JSON responses and SSE, model alias normalization, tool request/response conversion and streamed arguments including empty objects.
- Optional SSO: open Admin works without accounts; enabled OIDC rejects anonymous requests and verifies state, nonce and PKCE with a signing IdP fixture.
- SSO account provisioning, Admin/Viewer/payload permissions, immediate permission changes and local session invalidation on logout.
- Gateway key authentication, model permissions, rotation, revocation and browser mutation/origin restrictions.
- RPM limits, unavailable Redis failing closed, simultaneous budget reservations, idempotent settlement and conservative recovery of interrupted requests.
- Full encrypted client/upstream capture, SSE ordering, captured provider retry attempts, archive readback, retention, filesystem traversal rejection and S3 SDK round trip against an HTTP fixture.
- Initial capture failure blocks upstream execution; archive failure preserves the database journal; incomplete streams and client cancellation record incomplete usage/state.
- Browser: anonymous Admin → create key → send completion → inspect request/response → rotate key → reject old token → revoke key → audit event. Desktop/mobile screenshots were inspected, with no page errors or mobile page overflow.
- UI review: server-side authorization remains authoritative, independent dashboard requests are parallel, effects cancel obsolete fetches, payloads load on demand, inputs have labels and rendered payload text is escaped.

## Not verified against external systems

- Real OpenAI/Anthropic accounts and their selected models/prices: operator credentials and model configuration are intentionally unset.
- Organization IdP and real S3 deployment: only protocol fixtures were used.
- A real Redis server: atomic Lua behavior was exercised using miniredis, not a live Redis deployment.
- Docker image build/start: the host Docker daemon was unavailable. Compose syntax/config was validated; containers were not launched.
- CI workflow: written to reproduce the local checks, but not executed on GitHub.
- Performance/load characteristics and invoice-grade reconciliation: no benchmark or provider invoice reconciliation was performed.

See architecture.md for capture bounds, estimated budget accounting and retention semantics. This verification is a local Phase 1 implementation check, not a claim of production certification.

## Workspace enhancements

The following additions were verified against the local mock-provider preview:

- Go race tests and vet passed with new analytics tests for range/model/key filters, catalog exclusion, zero-filled empty buckets and real streaming first-output timestamps.
- Seven frontend unit tests passed: fragmented UTF-8/CRLF SSE, incomplete stream detection, tool argument assembly, captured-conversation reconstruction, lossless capture-byte decoding, malformed draft rejection and gateway error handling.
- Three browser flows passed: existing key/inference/payload/rotation flow; Playground → Inspector conversation/attempts/timing → replay draft → analytics; function arguments/tool results and Escape-to-close behavior.
- TypeScript typecheck and optimized Next production build passed. Desktop/mobile screenshots were inspected, including the native Inspector drawer and responsive Overview/Playground layouts.
- Visual inspection found the single-sample latency case: the chart now renders isolated samples as dots and resets its inspected bucket when a new dataset arrives, retaining missing-data gaps.
- Preview schema was preserved during backend reload, and migration 3 adds request operation/first-output fields without deleting request records. The public preview continues on port 18080 with simulated providers and optional SSO disabled.

Playground credentials remain in component memory; copied cURL uses an environment variable. Opening a captured request in Playground does not send it until Run is pressed. No paid model calls, external IdP changes or production deployment were performed.
