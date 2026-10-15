package mockthaid

import (
	"html/template"
	"net/http"
)

const layout = `{{define "head"}}<!doctype html>
<html lang="th"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Mock ThaID</title>
<style>
:root{--bg:#eef3fa;--card:#fff;--ink:#14213d;--muted:#5b6b82;--brand:#1d4ed8;--ok:#15803d;--bad:#b91c1c;--line:#d7e0ec}
@media (prefers-color-scheme:dark){:root{--bg:#0f172a;--card:#16213a;--ink:#e6ecf5;--muted:#9aabc4;--brand:#60a5fa;--ok:#4ade80;--bad:#f87171;--line:#2a3a57}}
*{box-sizing:border-box}
body{margin:0;font-family:"Sarabun","Noto Sans Thai",system-ui,sans-serif;background:var(--bg);color:var(--ink);line-height:1.6}
.banner{background:#f59e0b;color:#1f1300;text-align:center;font-size:14px;padding:6px 16px;font-weight:600}
main{max-width:460px;margin:24px auto;padding:0 16px}
.card{background:var(--card);border:1px solid var(--line);border-radius:16px;padding:24px}
h1{font-size:22px;margin:0 0 4px}
p.sub{color:var(--muted);margin:0 0 16px}
#qr{display:flex;justify-content:center;padding:12px;background:#fff;border-radius:12px;margin:16px 0}
#qr svg{width:240px;height:240px}
.chips span{display:inline-block;border:1px solid var(--line);border-radius:999px;padding:2px 10px;margin:2px;font-size:13px;color:var(--muted)}
label{display:block;font-weight:600;margin:14px 0 6px}
select,input{width:100%;padding:10px 12px;border-radius:10px;border:1px solid var(--line);background:var(--bg);color:var(--ink);font:inherit}
.row{display:flex;gap:10px;margin-top:20px}
button,.btn{flex:1;padding:12px;border-radius:10px;border:0;font:inherit;font-weight:600;cursor:pointer;text-align:center;text-decoration:none}
.primary{background:var(--brand);color:#fff}
.ghost{background:transparent;border:1px solid var(--line);color:var(--ink)}
.err{color:var(--bad);font-weight:600}
.ok{color:var(--ok)}
.muted{color:var(--muted);font-size:14px}
table{width:100%;border-collapse:collapse;font-size:14px}td{padding:4px 0;border-bottom:1px solid var(--line)}
</style></head><body>
<div class="banner">จำลอง ThaID สำหรับทดสอบเท่านั้น — ไม่ใช่ระบบจริงของกรมการปกครอง</div>
<main><div class="card">{{end}}
{{define "foot"}}</div></main></body></html>{{end}}`

var (
	homeTmpl = page(`{{template "head"}}
<h1>Mock ThaID Provider</h1><p class="sub">Issuer: {{.Issuer}}</p>
<p>บุคคลทดสอบ (PID ขึ้นต้นด้วย 0 ซึ่งไม่ออกให้ประชาชนจริง):</p>
<table>{{range .Personas}}<tr><td>{{.Name}}</td><td>{{.PID}}</td></tr>{{end}}</table>
<p class="muted">PIN ทดสอบ: {{.PIN}}</p>
{{template "foot"}}`)

	qrTmpl = page(`{{template "head"}}
<div id="auth" data-req="{{.ID}}">
<h1>ยืนยันตัวตนด้วย ThaID</h1>
<p class="sub">{{.Client}} ขอยืนยันตัวตนของคุณ</p>
<p>สแกน QR Code นี้ด้วยแอป ThaID บนโทรศัพท์</p>
<div id="qr"><span class="muted">กำลังสร้าง QR Code…</span></div>
<p class="muted">ข้อมูลที่ขอ: <span class="chips">{{range .Scopes}}<span>{{.}}</span>{{end}}</span></p>
<p id="msg" class="muted">รอการยืนยันจากโทรศัพท์… (หมดอายุใน <span id="left">{{.TTL}}</span> วินาที)</p>
<div class="row"><a class="btn ghost" id="open" href="{{.ScanURL}}" target="_blank" rel="noopener">เปิดบนเครื่องนี้ (จำลองการสแกน)</a></div>
</div>
<script src="https://cdnjs.cloudflare.com/ajax/libs/qrcode-generator/1.4.4/qrcode.min.js"></script>
<script>
(function(){
  var id = {{.ID}}, scanURL = {{.ScanURL}}, left = {{.TTL}};
  var box = document.getElementById('qr'), msg = document.getElementById('msg');
  function goTop(u){ try { top.location.replace(u); } catch (e) { location.replace(u); } }
  try {
    var q = qrcode(0, 'M'); q.addData(scanURL); q.make();
    box.innerHTML = q.createSvgTag(6, 2);
  } catch (e) {
    box.textContent = 'สร้าง QR ไม่ได้ (ไม่มีอินเทอร์เน็ตสำหรับโหลดไลบรารี) ใช้ลิงก์: ' + scanURL;
  }
  var timer = setInterval(function(){ if (left > 0) document.getElementById('left').textContent = --left; }, 1000);
  function poll(){
    fetch('status?id=' + encodeURIComponent(id), {cache:'no-store'})
      .then(function(r){ return r.json(); })
      .then(function(s){
        if (s.status === 'approved' || s.status === 'denied') {
          msg.textContent = s.status === 'approved' ? 'ยืนยันสำเร็จ กำลังกลับไปยังเว็บไซต์…' : 'ผู้ใช้ปฏิเสธคำขอ';
          clearInterval(timer); goTop(s.redirect); return;
        }
        if (s.status === 'expired') {
          clearInterval(timer); box.innerHTML = '';
          msg.innerHTML = '<span class="err">QR Code หมดอายุ กรุณากลับไปเริ่มเข้าสู่ระบบใหม่</span>';
          try { parent.postMessage({type:'thaid-qr-expired'}, '*'); } catch (e) {}
          return;
        }
        setTimeout(poll, 1500);
      })
      .catch(function(){ setTimeout(poll, 3000); });
  }
  poll();
})();
</script>
{{template "foot"}}`)

	scanTmpl = page(`{{template "head"}}
<h1>แอป ThaID (จำลอง)</h1>
<p class="sub"><b>{{.Client}}</b> ขอยืนยันตัวตนของคุณ</p>
<p class="muted">ข้อมูลที่จะส่งให้เว็บไซต์: <span class="chips">{{range .Scopes}}<span>{{.}}</span>{{end}}</span></p>
{{with .Error}}<p class="err">{{.}}</p>{{end}}
<form method="post" action="scan">
<input type="hidden" name="req" value="{{.ID}}">
<label for="persona">บุคคลทดสอบ</label>
<select id="persona" name="persona">{{$sel := .Selected}}{{range .Personas}}<option value="{{.PID}}"{{if eq .PID $sel}} selected{{end}}>{{.Name}} ({{.PID}})</option>{{end}}</select>
<label for="pin">PIN 6 หลัก</label>
<input id="pin" name="pin" type="password" inputmode="numeric" autocomplete="off" maxlength="6" placeholder="PIN ทดสอบคือ {{.PIN}}">
<div class="row">
<button class="ghost" name="action" value="deny">ปฏิเสธ</button>
<button class="primary" name="action" value="approve">ยืนยัน</button>
</div>
</form>
{{template "foot"}}`)

	doneTmpl = page(`{{template "head"}}
{{if .Approved}}<h1 class="ok">ยืนยันตัวตนสำเร็จ</h1><p>{{.Name}}</p>
<p class="muted">กลับไปที่หน้าจอคอมพิวเตอร์ เว็บไซต์จะเข้าสู่ระบบให้อัตโนมัติ</p>
{{else}}<h1 class="err">ปฏิเสธคำขอแล้ว</h1><p class="muted">เว็บไซต์จะไม่ได้รับข้อมูลของคุณ</p>{{end}}
{{template "foot"}}`)

	errorTmpl = page(`{{template "head"}}<h1 class="err">เกิดข้อผิดพลาด</h1><p>{{.}}</p>{{template "foot"}}`)
)

func page(body string) *template.Template {
	return template.Must(template.Must(template.New("layout").Parse(layout)).New("page").Parse(body))
}

func render(w http.ResponseWriter, code int, t *template.Template, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_ = t.ExecuteTemplate(w, "page", data)
}
