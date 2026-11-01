# Relay · LLM Gateway

Self-hosted LLM gateway ด้วย Go (`net/http` + chi) พร้อม Next.js Admin UI, PostgreSQL, Redis และ encrypted request/response capture

## Phase 1

- OpenAI-compatible `/v1/chat/completions` และ `/v1/models`
- OpenAI / Anthropic adapters: text, JSON responses, SSE และ function tool calls
- Model aliases ที่แก้ไขผ่าน Admin UI/API และบันทึกใน PostgreSQL
- API keys แบบ hash: สร้าง, หมุน, revoke, model allowlist, RPM, concurrency และ monthly budget
- **SSO optional**: `auth.mode: disabled` เข้า Admin ได้ทันที; `oidc` ใช้ SSO พร้อม session และ role
- Admin / Viewer และ permission อ่าน payload แยกต่างหาก
- บันทึก client/upstream requests, responses และ SSE events แบบเข้ารหัสก่อนส่งต่อ
- ค้นหา request history ตาม model, key, owner, state และช่วงเวลา พร้อม Live/Pause
- Request Inspector: Conversation, Raw JSON, Tools, Attempts และ Timing waterfall
- Playground: ทดลอง JSON/SSE, ปรับ model/parameters, Copy cURL และเปิด request ย้อนหลัง
- Overview: traffic/errors/P95 latency/spend ช่วง 24h/7d/30d พร้อม breakdown ตาม model/key
- Worker สำหรับ archive, retention และกู้สถานะคำขอที่ process หยุดกลางทาง
- Audit trail, health/readiness, Prometheus metrics และ optional OTLP traces

อ่าน [ขอบเขต API](docs/compatibility.md) และ [ข้อรับประกันการบันทึก/งบประมาณ](docs/architecture.md) ก่อนใช้งานจริง

## เริ่มด้วย Docker Compose

ต้องมี Docker Engine/Compose, OpenSSL และ Python 3 สำหรับ setup script

```sh
./scripts/setup-local.sh
docker compose --env-file .env -f deploy/compose.yaml up --build
```

เปิด [Admin UI](http://localhost:8080/admin) ได้ทันทีโดยไม่ต้อง login ค่าเริ่มต้น bind port ไว้ที่ loopback และ SSO ปิดอยู่

1. ใส่ provider credentials ใน `.env` และ restart gateway เมื่อแก้ค่า
2. ในหน้า Models เปลี่ยน placeholder เป็น model ID ที่บัญชี provider ของคุณเข้าถึงได้ และตั้งราคา USD / million tokens
3. ในหน้า API keys สร้าง key พร้อม model allowlist, RPM, concurrency และ budget
4. เก็บ credential ที่แสดงครั้งเดียว แล้วนำไปใช้กับ client ของคุณ

Provider credentials มาจาก environment/config ของ operator; UI ไม่ส่งหรืออ่าน credentials กลับมา
ค่า budget `0` หมายถึงไม่จำกัดวงเงิน; keys ที่มี budget ต้องใช้ model ที่ตั้งราคาแล้ว

```sh
# ตั้ง GATEWAY_API_KEY ผ่าน shell/session ของคุณก่อน
curl http://localhost:8080/v1/chat/completions \
  -H "Authorization: Bearer $GATEWAY_API_KEY" \
  -H 'Content-Type: application/json' \
  -d '{"model":"chat-fast","messages":[{"role":"user","content":"สวัสดี"}],"max_tokens":256}'
```

ดู `.env.example` และ `configs/example.yaml` สำหรับค่าทั้งหมด Compose ใช้ `configs/example.yaml`; การรันด้วย Go โดยตรงใช้ `configs/local.yaml`
Model config ใน YAML seed เฉพาะ aliases ที่ยังไม่มี การแก้ aliases ที่มีอยู่ให้ใช้ Admin UI/API

## รันด้วย Go / Node โดยตรง

ต้องมี Go 1.26.10+, Node 24+, pnpm 11.25.0, PostgreSQL และ Redis ที่รันอยู่

```sh
./scripts/setup-local.sh
# แก้ DATABASE_URL / REDIS_URL ใน .env ให้ตรงกับระบบ local
set -a
. ./.env
set +a
make migrate
make run
```

เปิด terminal เพิ่มสำหรับ worker และ UI โดยโหลด `.env` แบบเดียวกัน:

```sh
make worker
# อีก terminal
cd web
pnpm install --frozen-lockfile
pnpm dev
```

เข้า UI ผ่าน gateway ที่ `http://localhost:8080/admin` เพื่อให้ UI และ API อยู่ origin เดียวกัน

หากเปลี่ยน origin ให้แก้ `public_url` หรือ `GATEWAY_PUBLIC_URL` ด้วย ส่วน `GATEWAY_LISTEN` และ `GATEWAY_WEB_URL` ใช้ override สำหรับ container ได้

## เปิด SSO เมื่อต้องการ

ใน config ที่ใช้งาน:

```yaml
auth:
  mode: oidc
  issuer: "${OIDC_ISSUER}"
  client_id: "${OIDC_CLIENT_ID}"
  client_secret: "${OIDC_CLIENT_SECRET}"
  admin_subjects: ["subject-from-your-idp"]
  payload_subjects: []
```

ตั้ง redirect URI ใน IdP เป็น `<public_url>/auth/callback` แล้ว restart gateway
ผู้ใช้ที่เข้าครั้งแรกจะเป็น Viewer ยกเว้น subject ที่อยู่ใน `admin_subjects` ซึ่งจะได้สิทธิ์ Admin และอ่าน payload ได้ การกำหนดใน config ใช้ตอนสร้าง account ครั้งแรก; หลังจากนั้นแก้สิทธิ์ผ่านหน้า Users

โหมด `disabled` ใช้ actor `anonymous` ที่มีสิทธิ์ Admin และอ่าน payload ได้ ไม่ต้องสร้าง user/session ส่วน `/v1/*` ใช้ gateway API key เสมอ ไม่ขึ้นกับ SSO

OIDC ใช้ authorization code + PKCE + state + nonce, session อายุ 8 ชั่วโมง และ HttpOnly/SameSite cookie (Secure เมื่อ public URL เป็น HTTPS) Logout ลบ local session; ไม่ได้ logout จาก IdP ทั้งองค์กร

## Request capture และ storage

- PostgreSQL เป็น durable journal ของ encrypted events
- Worker ย้าย events ไป filesystem archive (local/Compose) หรือ S3-compatible storage
- S3 bucket ต้องสร้างไว้ก่อน ตั้ง `capture.storage: s3`, `endpoint` เป็น host[:port] ไม่มี scheme และเลือก `secure` ให้ตรงกับ endpoint
- Gateway และ worker ต้องใช้ encryption key เดียวกันและเข้าถึง archive ชุดเดียวกัน
- เก็บ `GATEWAY_ENCRYPTION_KEY` ไว้อย่างถาวรและสำรองพร้อม database/archive; การเปลี่ยนค่านี้ไม่ได้ re-encrypt logs เดิม
- Retention เริ่มต้น 30 วัน ลบ request metadata, attempts, payload และ ledger ของ request ที่หมดอายุ; monthly budget aggregates ยังคงอยู่ Audit trail แยกจาก retention นี้
- ไม่บันทึก Authorization, cookies หรือ provider API keys ใน capture metadata แต่ prompt/response อาจมีข้อมูลที่ผู้ใช้ส่งมาเองตามข้อกำหนด full capture

## ตรวจสอบ

```sh
go test -race ./...
go vet ./...
cd web && pnpm test:unit && pnpm typecheck && pnpm build
```

Integration tests ต้องใช้ฐานข้อมูลสำหรับทดสอบโดยเฉพาะ แต่ละ fixture สร้างและลบ schema ของตัวเอง ใช้ Redis-compatible miniredis และ HTTP/OIDC/S3 simulators โดยไม่มีการเรียก LLM จริง

```sh
TEST_DATABASE_URL='postgres://gateway:gateway@localhost:5432/gateway_test?sslmode=disable' \
  go test -race ./...
```

Browser tests ครอบคลุม Admin → keys → inference → payload, Playground → Inspector → replay → Overview และ tool calls:

```sh
cd web
pnpm exec playwright install chromium
TEST_DATABASE_URL='postgres://gateway:gateway@localhost:5432/gateway_test?sslmode=disable' pnpm test:e2e
```

Browser fixture ใช้ port 18080 และ Next ใช้ 13000 หากมี Chromium อยู่แล้วตั้ง `PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH` ได้

Generate database queries หลังแก้ SQL:

```sh
go run github.com/sqlc-dev/sqlc/cmd/sqlc@v1.31.1 generate
```

## Observability

`/healthz` ตรวจ process, `/readyz` ตรวจ PostgreSQL/Redis และ `/metrics` ใช้ Admin authorization ตาม auth mode
ตั้ง `OTEL_EXPORTER_OTLP_ENDPOINT` เพื่อเปิด HTTP OTLP trace export; ไม่ตั้งค่านี้ tracing จะปิด ข้อมูล prompt/response ไม่ถูกใส่ใน traces

ดู [architecture](docs/architecture.md), [API compatibility](docs/compatibility.md), [OpenAPI](api/openapi.yaml) และ [verification](docs/verification.md)

## ใช้สามหน้าใหม่

- **Overview** เลือกช่วงเวลาและ model/key ได้ คลิกชื่อใน breakdown เพื่อเปิด Request history ที่กรองแล้ว กราฟใช้ inference requests เท่านั้น ไม่นับ `/v1/models` และไม่มีการสร้าง sample traffic อัตโนมัติ
- **Request history** อัปเดตทุก 3 วินาทีเมื่อเปิด Live และไม่ได้ดูหน้าประวัติย้อนหลัง เปิด request เพื่อดู Inspector; การโหลดเนื้อหายังคงตรวจ payload permission และบันทึก audit ทุกครั้ง
- **Inspector** อ่านบทสนทนา, JSON/events, function arguments/results, retry attempts และ timing ได้ ใช้ Open in Playground เพื่อนำ request มาเป็น draft โดยไม่ส่งซ้ำทันที
- **Playground** ใส่ gateway API key ที่มีสิทธิ์ใช้ alias (เก็บเฉพาะใน memory ของหน้านี้), ปรับ system/user message และ parameters แล้วกด Run รองรับ Stop generation และ Continue conversation; tool calls แสดงผลให้ตรวจสอบ ไม่ได้ execute tools อัตโนมัติ
- Copy cURL ใช้ `$GATEWAY_API_KEY` แทน credential จริง ส่วน Advanced request JSON ต้องกด Apply JSON ก่อน Run
- เมื่ออัปเกรด ให้รัน migration ก่อนเริ่ม backend เพื่อเพิ่ม `operation` และ `first_output_at` ข้อมูล timing เดิมที่ไม่มีการวัดจะแสดงเป็น “—”
