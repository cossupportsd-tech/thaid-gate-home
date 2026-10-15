package webapp

import (
	"html/template"
	"net/http"
)

const layout = `{{define "head"}}<!doctype html>
<html lang="th"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>{{.App}}</title>
<style>
:root{--bg:#f5f7fb;--card:#fff;--ink:#111827;--muted:#6b7280;--brand:#0f766e;--bad:#b91c1c;--line:#e5e7eb;--soft:#f0fdfa}
@media (prefers-color-scheme:dark){:root{--bg:#0b1220;--card:#131c2e;--ink:#e5e7eb;--muted:#9ca3af;--brand:#2dd4bf;--bad:#f87171;--line:#263248;--soft:#0f2a2a}}
*{box-sizing:border-box}
body{margin:0;font-family:"Sarabun","Noto Sans Thai",system-ui,sans-serif;background:var(--bg);color:var(--ink);line-height:1.6}
header{padding:14px 16px;border-bottom:1px solid var(--line);background:var(--card);display:flex;justify-content:space-between;align-items:center;gap:12px}
header b{font-size:17px}
main{max-width:{{if .Wide}}760px{{else}}520px{{end}};margin:32px auto;padding:0 16px}
.card{background:var(--card);border:1px solid var(--line);border-radius:16px;padding:28px;margin-bottom:18px}
h1{font-size:24px;margin:0 0 6px}
h2{font-size:18px;margin:0 0 4px}
.muted{color:var(--muted)}
.small{font-size:14px}
.err{background:color-mix(in srgb,var(--bad) 12%,transparent);color:var(--bad);padding:10px 14px;border-radius:10px;margin:14px 0;word-break:break-word}
.btn{display:block;width:100%;padding:14px;border-radius:12px;border:0;background:var(--brand);color:#fff;font:inherit;font-weight:700;text-align:center;text-decoration:none;cursor:pointer;margin-top:18px}
.btn.ghost{background:transparent;color:var(--ink);border:1px solid var(--line);font-weight:600}
.ok{display:inline-block;background:color-mix(in srgb,var(--brand) 15%,transparent);color:var(--brand);padding:2px 10px;border-radius:999px;font-size:14px;font-weight:600}
table{width:100%;border-collapse:collapse;margin-top:12px;font-size:15px}
th,td{text-align:left;padding:9px 8px;border-bottom:1px solid var(--line);vertical-align:top}
th{color:var(--muted);font-weight:600;font-size:13px}
td.v{font-weight:600;word-break:break-word}
td.k{width:38%}
code{font-size:12px;color:var(--muted)}
.src{display:inline-block;font-size:11px;border:1px solid var(--line);border-radius:999px;padding:0 7px;margin:2px 2px 0 0;color:var(--muted);white-space:nowrap}
.steps{margin:8px 0 0;padding-left:20px}
.steps li{margin:4px 0}
.bm{display:inline-block;padding:4px 10px;border:1px dashed var(--brand);border-radius:8px;color:var(--brand);text-decoration:none;font-weight:600}
input.url{width:100%;padding:10px 12px;border-radius:10px;border:1px solid var(--line);background:var(--bg);color:var(--ink);font:inherit}
.scanbox{display:flex;justify-content:center;margin:18px 0}
.scanbox iframe{width:360px;height:540px;border:1px solid var(--line);border-radius:14px;background:#fff}
/* "login succeeded" window: shown on first load, closed with #close without JavaScript */
.modal{position:fixed;inset:0;background:rgba(15,23,42,.55);display:flex;align-items:center;justify-content:center;padding:16px;z-index:10}
#close:target + .modal{display:none}
.modal .box{background:var(--card);border-radius:20px;padding:32px 28px;max-width:400px;width:100%;text-align:center;box-shadow:0 20px 60px rgba(0,0,0,.3);animation:pop .25s ease-out}
.tick{width:72px;height:72px;border-radius:50%;background:var(--brand);color:#fff;font-size:40px;line-height:72px;margin:0 auto 14px}
@keyframes pop{from{transform:scale(.85);opacity:0}to{transform:scale(1);opacity:1}}
</style></head><body>
<header><b>{{.App}}</b><span class="muted small">ป้องกันด้วย ThaID</span></header>
<main><div class="card">{{end}}
{{define "foot"}}</div></main></body></html>{{end}}`

var (
	loginTmpl = page(`{{template "head" .}}
<h1>ยืนยันตัวตนก่อนเข้าใช้งาน</h1>
<p class="muted">เว็บไซต์นี้เปิดให้เฉพาะผู้ที่ยืนยันตัวตนด้วยแอป ThaID แล้ว กดปุ่มด้านล่าง แล้วสแกน QR Code ด้วยแอป ThaID บนโทรศัพท์</p>
{{with .Error}}<div class="err">{{.}}</div>{{end}}
<a class="btn" href="/auth/thaid/start">เข้าสู่ระบบด้วย ThaID</a>
{{if .Paste}}
<div style="margin-top:22px;border-top:1px solid var(--line);padding-top:16px">
<p style="margin:0"><b>โหมดทดสอบ</b> <span class="muted small">Callback ที่ลงทะเบียนไว้กับ ThaID อยู่บนเครื่องอื่น หลังยืนยันในแอป เบราว์เซอร์จะไปที่หน้านั้น (ขึ้น 404) ให้ส่งกลับมาที่นี่ภายใน 1 นาที ด้วยวิธีใดวิธีหนึ่ง:</span></p>
<ol class="steps small">
<li><b>ง่ายที่สุด:</b> ลากปุ่มนี้ไปวางที่แถบ Bookmarks ครั้งเดียว → <a class="bm" href="{{.Bookmarklet}}">ส่งกลับ ThaID Gate</a><br><span class="muted">เมื่อขึ้นหน้า 404 ให้กด Bookmark นี้</span></li>
<li>หรือแก้แถบที่อยู่: เปลี่ยน <code>http://applicant.sd.intranet</code> เป็น <code>{{.Base}}</code> แล้วกด Enter</li>
<li>หรือคัดลอก URL ทั้งหมดมาวางในช่องด้านล่าง</li>
</ol>
<form method="post" action="/auth/thaid/paste">
<input class="url" name="url" placeholder="http://.../callback?code=...&amp;state=..." autocomplete="off">
<button class="btn ghost">ยืนยันด้วย URL ที่วาง</button>
</form>
</div>
{{end}}
{{template "foot"}}`)

	scanTmpl = page(`{{template "head" .}}
<h1>สแกน ThaID เพื่อเข้าสู่ระบบ</h1>
<p class="muted">เปิดแอป <b>ThaID</b> บนโทรศัพท์ แล้วสแกน QR Code ด้านล่าง จากนั้นกดยืนยันตัวตนในแอป หน้าเว็บนี้จะเข้าสู่ระบบให้อัตโนมัติเมื่อคุณยืนยันสำเร็จ</p>
<div class="scanbox" id="scan" data-ttl="{{.TTL}}" data-origin="{{.ProviderOrigin}}">
<iframe src="{{.AuthURL}}" title="QR Code สำหรับสแกนด้วยแอป ThaID" loading="eager" referrerpolicy="no-referrer"></iframe>
</div>
<p class="small muted" id="scanmsg" style="text-align:center">ระบบจะต่อ QR ให้อัตโนมัติเมื่อใกล้หมดอายุ (นับถอยหลัง <span id="scanleft">{{.TTL}}</span> วินาที)</p>
<p class="small muted" style="text-align:center">ถ้าไม่เห็น QR Code ให้ <a href="{{.AuthURL}}" target="_blank" rel="noopener">เปิดหน้าสแกนในแท็บใหม่</a> หรือ <a href="/scan">ลองใหม่อีกครั้ง</a></p>
<script nonce="{{.Nonce}}">
(function(){
  var box = document.getElementById('scan');
  var ttl = parseInt(box.getAttribute('data-ttl'), 10) || 300;
  var origin = box.getAttribute('data-origin') || '';
  var leftEl = document.getElementById('scanleft'), msg = document.getElementById('scanmsg');
  var left = ttl, done = false;
  function refreshNow(){
    if (done) return; done = true;
    if (msg) msg.textContent = 'QR กำลังหมดอายุ กำลังสร้าง QR ใหม่…';
    location.replace('/scan');
  }
  var timer = setInterval(function(){
    if (left > 0) { left--; if (leftEl) leftEl.textContent = left; }
    if (left <= 5) { clearInterval(timer); refreshNow(); }
  }, 1000);
  addEventListener('message', function(e){
    if (origin && e.origin === origin && e.data && e.data.type === 'thaid-qr-expired') {
      clearInterval(timer); refreshNow();
    }
  });
})();
</script>
{{template "foot"}}`)

	appTmpl = page(`{{template "head" .}}
{{if .Welcome}}<span id="close"></span>
<div class="modal" role="dialog" aria-modal="true" aria-labelledby="ok-title">
<div class="box">
<div class="tick">✓</div>
<h1 id="ok-title">เข้าสู่ระบบสำเร็จ</h1>
<p style="margin:6px 0 2px;font-size:18px;font-weight:600">{{.Name}}</p>
<p class="muted small" style="margin:0">ยืนยันตัวตนด้วย ThaID เมื่อ {{.LoginAt}}</p>
<a class="btn" href="#close">ดูข้อมูลที่ได้รับ</a>
</div>
</div>{{end}}
<span class="ok">ยืนยันตัวตนแล้ว</span>
<h1 style="margin-top:10px">ยินดีต้อนรับ {{.Name}}</h1>
<p class="muted" style="margin:0">เลขประจำตัวประชาชน {{.PID}} · เข้าสู่ระบบเมื่อ {{.LoginAt}} · Session หมดอายุ {{.Expires}} น.</p>
</div>

<div class="card" id="data">
<h2>ข้อมูลบุคคลที่ได้รับจาก ThaID</h2>
<p class="muted small" style="margin:0">ได้รับเฉพาะข้อมูลตาม Scope ที่กรมการปกครองอนุมัติให้ระบบนี้</p>
{{if .Person}}<table>
<tr><th>ข้อมูล</th><th>ค่า</th><th>แหล่งที่มา</th></tr>
{{range .Person}}<tr><td class="k">{{.Label}}<br><code>{{.Key}}</code></td><td class="v">{{.Value}}</td><td>{{range .Sources}}<span class="src">{{.}}</span>{{end}}</td></tr>
{{end}}</table>{{else}}<p class="err">ThaID ไม่ได้ส่งข้อมูลบุคคลมา</p>{{end}}
{{with .UserinfoErr}}<p class="err small">เรียก UserInfo ไม่สำเร็จ: {{.}}</p>{{end}}
</div>

<div class="card">
<h2>ข้อมูลการยืนยันตัวตน</h2>
<table>
<tr><th>ข้อมูล</th><th>ค่า</th><th>แหล่งที่มา</th></tr>
{{range .Technical}}<tr><td class="k">{{.Label}}</td><td class="v">{{.Value}}</td><td>{{range .Sources}}<span class="src">{{.}}</span>{{end}}</td></tr>
{{end}}</table>
<form method="post" action="/logout"><button class="btn ghost">ออกจากระบบ</button></form>
{{template "foot"}}`)

	deniedTmpl = page(`{{template "head" .}}
<h1>ไม่มีสิทธิ์เข้าใช้งาน</h1>
<p>ยืนยันตัวตนด้วย ThaID สำเร็จ ({{.Name}}, {{.PID}}) แต่บัญชีนี้ไม่อยู่ในรายชื่อผู้มีสิทธิ์เข้าใช้งานระบบ</p>
<p class="muted">หากคิดว่าเป็นความผิดพลาด กรุณาติดต่อผู้ดูแลระบบ</p>
<a class="btn ghost" href="/login">กลับหน้าเข้าสู่ระบบ</a>
{{template "foot"}}`)

	notFoundTmpl = page(`{{template "head" .}}
<h1>404 — ไม่พบหน้าที่เรียก</h1>
<p style="font-size:28px;font-weight:700;margin:18px 0">สวัสดีครับ ชาวโลก</p>
<p class="muted small">path ที่เรียก: <code>{{.Path}}</code></p>
<a class="btn ghost" href="/login">กลับหน้าเข้าสู่ระบบ</a>
{{template "foot"}}`)
)

func page(body string) *template.Template {
	return template.Must(template.Must(template.New("layout").Parse(layout)).New("page").Parse(body))
}

func (s *Server) render(w http.ResponseWriter, code int, t *template.Template, data map[string]any) {
	data["App"] = s.cfg.AppName
	data["Paste"] = s.cfg.PasteCallback
	if _, ok := data["Wide"]; !ok {
		data["Wide"] = t == appTmpl
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(code)
	_ = t.ExecuteTemplate(w, "page", data)
}
