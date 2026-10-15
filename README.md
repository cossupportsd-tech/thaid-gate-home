# ThaID Gate — สแกน ThaID ยืนยันตัวตนก่อนเข้าเว็บไซต์

เว็บไซต์ตัวอย่างภาษา Go ที่ทุกหน้า **เข้าได้หลังผู้ใช้ยืนยันตัวตนด้วยแอป ThaID แล้วเท่านั้น**
ใช้ OpenID Connect (Authorization Code + PKCE) ตามที่ ThaID ของกรมการปกครองรองรับ
และมี **Mock ThaID** สำหรับทดสอบทั้งกระบวนการ รวมถึงสแกน QR ด้วยโทรศัพท์จริง โดยไม่ต้องใช้ข้อมูลประชาชนจริง

ใช้เฉพาะ Go standard library ไม่มี dependency ภายนอก

## เวอร์ชันนี้ (สำรอง — ไปหน้า ThaID ตรง ๆ)

โฟลเดอร์นี้เป็นสำเนาแยกต่างหากที่ปุ่ม login **พาไปหน้า ThaID แบบเต็มจอทันที** (`/auth/thaid/start`) โดย **ข้ามหน้า `/scan` ที่ฝัง QR ใน iframe** เพราะ ThaID จริงบล็อกการฝัง iframe (`/scan` จะขึ้น "content is blocked")

วิธีใช้ที่บ้าน:
1. สร้าง `config.local.env` (copy จาก `config.local.env.example`) ใส่ค่า ThaID production:
   - `THAID_ISSUER=https://imauth.bora.dopa.go.th`
   - `THAID_CLIENT_ID` / `THAID_CLIENT_SECRET` ตามที่ได้รับ
   - `THAID_REDIRECT_URL=<redirect_uri ที่ลงทะเบียน ตรงกันทุกตัวอักษร>` (เช่น `http://applicant.sd.intranet/callback`)
   - `THAID_PASTE_CALLBACK=true` และ `LISTEN=127.0.0.1:8081`
2. รัน `go run ./cmd/webapp` แล้วเปิด http://localhost:8081
3. กด **เข้าสู่ระบบด้วย ThaID** → สแกน + ใส่ PIN + ยืนยันบนแอป
4. ThaID จะพาเบราว์เซอร์ไป `redirect_uri?code=…&state=…` (เครื่องที่บ้านอาจเปิดหน้านั้นไม่ได้/404) → **คัดลอก URL จากแถบที่อยู่** แล้วมาวางในช่อง "ยืนยันด้วย URL ที่วาง" ที่ `/login` เพื่อเข้าระบบ
5. ทางเลือกให้ callback กลับอัตโนมัติไม่ต้อง paste: แก้ `hosts` ให้ `applicant.sd.intranet → 127.0.0.1` แล้วรัน gate ที่พอร์ต 80 (`LISTEN=:80`) และเข้าผ่าน `http://applicant.sd.intranet`

> `config.local.env` ถูก git-ignore ไว้และ **มี client_secret** — ระวังอย่าแชร์โฟลเดอร์นี้โดยไม่จำเป็น

## ขั้นตอนการทำงาน

1. ผู้ใช้เปิดเว็บ หากยังไม่ได้ยืนยันตัวตนจะถูกส่งไปหน้า `/login`
2. กด **สแกน QR ด้วยแอป ThaID** ระบบจะเปิดหน้า `/scan` ของเว็บเอง ซึ่งแสดง QR Code ของ ThaID ฝังอยู่ในหน้า (หรือเลือก **เปิดหน้า ThaID เต็มจอ** เพื่อเปลี่ยนไปหน้า ThaID โดยตรง)
3. ผู้ใช้สแกน QR ด้วยแอป ThaID กรอก PIN และกดยืนยัน
4. หน้าเว็บบนคอมพิวเตอร์กลับมาที่ `/auth/thaid/callback` เอง
5. เว็บไซต์แลก code เป็น ID Token ตรวจลายเซ็น ES256 และตรวจ iss, aud, exp และ nonce
6. หากผ่าน (และอยู่ใน allowlist ถ้ามีการกำหนด) ระบบสร้าง Session Cookie แบบ HttpOnly แล้วเข้า `/app` ได้

หมายเหตุ: ThaID จริงเป็นผู้แสดง QR Code เอง เว็บไซต์ของเราไม่ต้องสร้าง QR

## หน้าสแกน QR ภายในแอป (`/scan`)

หลังกดปุ่มเข้าสู่ระบบ เว็บจะพาไปที่หน้า `/scan` ซึ่งเป็น **หน้าของตัวแอปเอง** ที่ฝังหน้า QR ของ ThaID ไว้ใน `<iframe>` ผู้ใช้สแกนและกดยืนยันบนโทรศัพท์แล้วเว็บจะกลับมายัง `/callback` และเข้าสู่ระบบให้อัตโนมัติ โดยไม่ต้องหลุดไปยังหน้าของผู้ให้บริการทั้งจอ

- เว็บยัง **ไม่ได้สร้าง QR เอง** — QR ที่เห็นมาจากการแสดงของ ThaID/mock ใน iframe
- เพื่อฝัง iframe ได้ เว็บจะตั้ง CSP `frame-src` อนุญาตเฉพาะ origin ของผู้ให้บริการ (อ่านอัตโนมัติจาก issuer ที่ตั้งไว้) จึงไม่เปิดให้ฝังโดเมนอื่น
- ต้องการโหมดเต็มจอแบบเดิม ใช้ลิงก์ `/auth/thaid/start` หรือปุ่ม "เปิดหน้า ThaID เต็มจอ"
- หมายเหตุ: หากผู้ให้บริการจริงตั้ง `X-Frame-Options`/`frame-ancestors` ไม่ให้ฝัง iframe ให้ใช้โหมดเต็มจอแทน (Mock รองรับทั้งสามแบบ)
- **ต่อ QR ให้อัตโนมัติเมื่อใกล้หมดอายุ:** หน้า `/scan` จะเริ่ม login ใหม่และวาด QR ทดแทนก่อนหมดอายุไม่กี่วินาที และจะรีเฟรชทันทีหากหน้า QR ของผู้ให้บริการแจ้งว่าหมดอายุ (ผ่าน postMessage) ผู้ใช้จึงไม่ต้องสแกน QR ที่ตายแล้ว สคริปต์นี้ได้รับอนุญาตด้วย CSP nonce ต่อการร้องขอ (ไม่เปิด inline script โดยทั่วไป) ตั้งอายุ QR ได้ผ่าน `THAID_SCAN_TTL` (วินาที ค่าเริ่มต้น 300)
- **หน้า 404 เป็นมิตร:** path ที่ไม่มีอยู่จริงจะแสดงหน้า 404 พร้อมข้อความ "สวัสดีครับ ชาวโลก" และ path ที่เรียก (`notFoundTmpl`)

## ทดสอบบนเครื่อง (Mock ThaID)

ต้องมี Go 1.24 ขึ้นไป เปิด 2 terminal ในโฟลเดอร์ `thaid-gate`

```powershell
go run ./cmd/mockthaid
```

```powershell
go run ./cmd/webapp
```

จากนั้นเปิด http://localhost:8081

- **ทดสอบบนเครื่องเดียว:** กดปุ่ม "เปิดบนเครื่องนี้ (จำลองการสแกน)" ใต้ QR
- **ทดสอบด้วยโทรศัพท์จริง:** ต่อ Wi-Fi วงเดียวกับคอมพิวเตอร์ แล้วสแกน QR ด้วยแอปกล้อง ลิงก์ใน QR ใช้ IP ของเครื่องใน LAN (ดูได้จาก log ของ mockthaid) ถ้า Windows ถามเรื่อง Firewall ให้กด Allow บน Private network
- **PIN ทดสอบ:** `123456`
- **บุคคลทดสอบ:** มี 3 คน เลขประจำตัวขึ้นต้นด้วย 0 ซึ่งไม่มีการออกให้ประชาชนจริง

| ชื่อ | PID |
|---|---|
| นายสมชาย ทดสอบ | 0000000000019 |
| นางสาวสมหญิง ตัวอย่าง | 0000000000027 |
| นายบุคคล ภายนอก | 0000000000035 |

ทดสอบการจำกัดสิทธิ์ (allowlist):

```powershell
$env:ALLOWED_PIDS="0000000000019,0000000000027"; go run ./cmd/webapp
```

เมื่อตั้งค่านี้ "นายบุคคล ภายนอก" จะยืนยันตัวตนผ่าน แต่ระบบจะแจ้งว่า **ไม่มีสิทธิ์เข้าใช้งาน**

## Automated tests

```powershell
go test ./...
```

| ชุดทดสอบ | สิ่งที่ตรวจ |
|---|---|
| `internal/e2e` | Login ครบวงจร, หน้าสแกนในแอป `/scan` (ฝัง QR แล้วล็อกอินอัตโนมัติ), PIN ผิด, ผู้ใช้ปฏิเสธ, ปลอม state, เปิด callback จาก browser อื่น (login CSRF), ใช้ callback หรือ code ซ้ำ, allowlist, redirect_uri ที่ไม่ได้ลงทะเบียน, logout |
| `internal/oidc` | ลายเซ็นผิด, alg none หรือ HS256, payload ถูกแก้, aud หรือ iss ผิด, token หมดอายุ, nonce ไม่ตรง, kid ไม่รู้จัก, จุดบนกราฟที่ไม่อยู่บน P-256 |

## รันโดยไม่ต้องพิมพ์ค่า (`config.local.env`)

`cmd/webapp` โหลดไฟล์คอนฟิกอัตโนมัติ ทำให้รันคำสั่งเดียวได้เลยโดยไม่ต้องใส่ environment ทุกครั้ง

```powershell
Copy-Item config.local.env.example config.local.env
# แก้ค่าใน config.local.env (ใช้ชื่อ KEY เดียวกับ environment variables)
go run ./cmd/webapp
```

- รูปแบบไฟล์เป็น `KEY=VALUE` ต่อบรรทัด (ข้ามบรรทัดว่างและที่ขึ้นต้นด้วย `#`, ค่ามี `=` หลายตัวได้)
- ถ้าตัวแปรนั้นถูกตั้งไว้ใน environment จริง **env จะชนะไฟล์** (ไฟล์เป็นเพียงค่าเริ่มต้น)
- ไม่มีไฟล์ `config.local.env` → ใช้ค่าเริ่มต้นแบบ Mock (`http://localhost:9000`) เหมือนเดิม
- ชี้ไปไฟล์อื่นได้ด้วย `THAID_CONFIG_FILE`; ตั้งเป็น `-` เพื่อปิดการโหลด
- `config.local.env` ถูก git-ignore ไว้ **ไม่ให้ commit `client_secret`** (มีแค่ `.example` ที่อยู่ในซอร์ส)

## เชื่อมกับ ThaID จริง

ต้องลงทะเบียน Relying Party กับกรมการปกครองก่อน เพื่อรับ `client_id` และ `client_secret`
และลงทะเบียน Redirect URI ซึ่งต้องเป็น HTTPS
จากนั้นตั้งค่า environment แล้วรันเฉพาะ `cmd/webapp` (ไม่ต้องรัน mock)

```powershell
$env:THAID_ISSUER="https://imauthsbx.bora.dopa.go.th"   # Sandbox; Production คือ https://imauth.bora.dopa.go.th
$env:THAID_CLIENT_ID="<client_id ที่ได้รับ>"
$env:THAID_CLIENT_SECRET="<client_secret ที่ได้รับ>"
$env:THAID_REDIRECT_URL="https://your-domain.go.th/auth/thaid/callback"
$env:THAID_SCOPES="openid pid name name_en"
go run ./cmd/webapp
```

| ตัวแปร | ค่าเริ่มต้น | ความหมาย |
|---|---|---|
| `THAID_ISSUER` | `http://localhost:9000` | ระบบอ่าน endpoint ทั้งหมดจาก `/.well-known/openid-configuration` |
| `THAID_CLIENT_ID` / `THAID_CLIENT_SECRET` | `demo-web` / `demo-secret` | ค่าที่ได้จากการลงทะเบียน |
| `THAID_REDIRECT_URL` | `http://localhost:8081/auth/thaid/callback` | ต้องตรงกับที่ลงทะเบียนทุกตัวอักษร |
| `THAID_SCOPES` | `openid pid name name_en ial` | ขอได้เฉพาะ scope ที่ได้รับอนุมัติ |
| `THAID_AUTH_METHOD` | `client_secret_basic` | ThaID รองรับ `client_secret_basic` และ `client_secret_post` |
| `THAID_USE_PKCE` | `true` | ถ้า Sandbox ไม่รับ ให้ตั้งเป็น `false` |
| `THAID_SCAN_TTL` | `300` (วินาที) | อายุ QR บนหน้า `/scan` ก่อนที่ระบบจะต่อ QR ให้อัตโนมัติ |
| `ALLOWED_PIDS` | (ว่าง = ทุกคนที่ยืนยันตัวตนผ่าน) | เลข 13 หลักที่อนุญาต คั่นด้วย comma |
| `LISTEN` | `127.0.0.1:8081` | address ที่เว็บรับการเชื่อมต่อ |
| `TLS_CERT` / `TLS_KEY` | (ว่าง) | ใส่เพื่อให้เว็บให้บริการ HTTPS เอง |

ค่า issuer, endpoint, ES256 และ auth method ข้างต้นตรวจสอบจาก discovery document ของ ThaID
ณ วันที่ 15 ต.ค. 2026 ส่วนเรื่องต่อไปนี้ **ยังไม่ได้ทดสอบกับ ThaID จริง** และต้องยืนยันใน Sandbox:

- ThaID รองรับ PKCE หรือไม่
- ID Token มี `nonce` กลับมาหรือไม่
- ชื่อ claim ของข้อมูลบุคคล

## ก่อนขึ้น Production

- [ ] ให้บริการผ่าน HTTPS (เมื่อ Redirect URL เป็น https ระบบจะตั้ง Cookie เป็น Secure เอง)
- [ ] ย้าย Session และ pending login จากหน่วยความจำไปเก็บใน Redis หรือ SQL Server หากมีมากกว่า 1 instance
- [ ] เก็บ `client_secret` ใน Secret Manager ห้ามใส่ใน source code
- [ ] ใช้สิทธิ์จากฐานข้อมูลบุคลากร (RBAC/ABAC) แทน `ALLOWED_PIDS`
- [ ] เก็บ Audit log และห้ามบันทึกเลขประจำตัวเต็ม (โค้ดนี้แสดงเฉพาะ 3 หลักท้าย)
- [ ] ถ้าใช้สถาปัตยกรรม DMZ/Internal: ให้เว็บนี้อยู่ใน DMZ เป็น broker ที่ออก Internet ได้ และให้ Internal ตรวจ ID Token ซ้ำ

## โครงสร้าง

```
cmd/webapp        เว็บไซต์ที่ต้องยืนยันตัวตนก่อนเข้า
cmd/mockthaid     ThaID จำลองสำหรับทดสอบ
internal/oidc     OIDC client: discovery, PKCE, token exchange, ตรวจ ES256 ID Token
internal/webapp   หน้าเว็บ, session, ตัวกั้นหน้าที่ต้อง login, allowlist
internal/mockthaid QR page, แอป ThaID จำลอง, token endpoint
internal/e2e      end-to-end tests
```
