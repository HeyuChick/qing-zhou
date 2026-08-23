package api

import (
	"fmt"
	"html/template"
	"net/http"
)

// GET /reset?token=... — the page the password-reset email actually links to.
//
// It is server-rendered for the same reason handleVerify is, plus one specific
// to it: the panel SPA is a HASH router, so a plain "/reset?token=..." path
// never reaches vue-router at all. It fell through to the SPA catch-all,
// index.html loaded, the router saw no hash and rewrote the location to "#/",
// and the user landed on the public monitor page with the token discarded. The
// whole 找回密码 flow dead-ended there: the mail went out, the link answered
// 200, and POST /api/auth/reset had no caller anywhere in the frontend.
//
// Keeping the URL shape the mail already uses is deliberate — reset links
// sitting in inboxes right now start working, not just ones sent from here on.
//
// The token is NOT consumed here: this handler only paints the form, and the
// POST redeems it. That also keeps the page from being an unauthenticated
// oracle that burns a token merely by being fetched, which matters because mail
// scanners and chat link-previewers do pre-fetch these.
func (a *API) handleResetPage(w http.ResponseWriter, r *http.Request) {
	// The token is a credential and this page embeds it: keep it out of shared
	// caches and out of search indexes.
	w.Header().Set("Cache-Control", "no-store, must-revalidate")
	w.Header().Set("X-Robots-Tag", "noindex, nofollow")

	token := r.URL.Query().Get("token")
	if token == "" {
		writeHTMLPage(w, http.StatusBadRequest, "链接无效",
			"这个重置链接不完整，请回到登录页重新发起「找回密码」。")
		return
	}
	// Whether the token is still good is deliberately not checked here — see
	// above. An expired one gets its "重置链接无效或已过期" from the POST, which
	// is the only place that can say so without spending it.
	writeResetFormPage(w, token)
}

// writeResetFormPage renders the set-a-new-password form, in the same visual
// shell as writeHTMLPage so both pages in this flow read as one product.
//
// token goes through template.HTMLEscapeString before it reaches the value
// attribute. It is raw query input, so interpolating it unescaped would be a
// reflected XSS on an unauthenticated page — and one an attacker can put in
// front of a victim simply by mailing them a "reset" link.
func writeResetFormPage(w http.ResponseWriter, token string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	fmt.Fprintf(w, resetPageHTML, template.HTMLEscapeString(token))
}

// %% escapes are literal percent signs for Fprintf; the single %s is the token.
const resetPageHTML = `<!doctype html><html lang="zh-CN"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>重置密码 - 黑羽短腿机场</title>
<style>body{font-family:system-ui,-apple-system,"Microsoft YaHei",sans-serif;background:#f5f6f8;margin:0;display:flex;min-height:100vh;align-items:center;justify-content:center}
.card{background:#fff;padding:32px 28px;border-radius:14px;box-shadow:0 6px 24px rgba(0,0,0,.08);width:100%%;max-width:360px;box-sizing:border-box}
h1{font-size:20px;margin:0 0 6px;color:#1f2937;text-align:center}
.sub{color:#6b7280;font-size:13px;line-height:1.6;margin:0 0 6px;text-align:center}
label{display:block;font-size:13px;color:#4b5563;margin:14px 0 6px}
input{width:100%%;box-sizing:border-box;padding:10px 12px;border:1px solid #d1d5db;border-radius:8px;font-size:14px}
input:focus{outline:none;border-color:#2563eb}
button{width:100%%;margin-top:20px;padding:11px;background:#2563eb;color:#fff;border:0;border-radius:8px;font-size:15px;cursor:pointer}
button:disabled{background:#9ca3af;cursor:not-allowed}
.msg{margin-top:14px;font-size:13px;line-height:1.6;text-align:center;min-height:19px}
.err{color:#dc2626}.ok{color:#059669}
a{color:#2563eb}</style></head>
<body><div class="card">
<h1>重置密码</h1>
<p class="sub">为你的黑羽短腿机场账号设置一个新密码。</p>
<form id="f" autocomplete="off">
<input type="hidden" id="tok" value="%s">
<label for="p1">新密码（至少 6 位）</label>
<input type="password" id="p1" autocomplete="new-password" required minlength="6">
<label for="p2">再输入一次</label>
<input type="password" id="p2" autocomplete="new-password" required minlength="6">
<button type="submit" id="btn">设置新密码</button>
</form>
<p class="msg" id="m"></p>
</div>
<script>
var f=document.getElementById('f'),m=document.getElementById('m'),btn=document.getElementById('btn');
f.addEventListener('submit',function(e){
  e.preventDefault();
  var p1=document.getElementById('p1').value,p2=document.getElementById('p2').value;
  if(p1.length<6){m.className='msg err';m.textContent='密码至少 6 位';return;}
  if(p1!==p2){m.className='msg err';m.textContent='两次输入的密码不一致';return;}
  btn.disabled=true;m.className='msg';m.textContent='提交中…';
  fetch('/api/auth/reset',{method:'POST',headers:{'Content-Type':'application/json'},
    body:JSON.stringify({token:document.getElementById('tok').value,new_password:p1})})
   .then(function(r){return r.json()})
   .then(function(j){
     // The success envelope is {code:0}; fail() puts the HTTP status in code.
     // See respond.go — 0 is the only success value, NOT 2xx.
     if(j.code===0){
       f.style.display='none';m.className='msg ok';
       m.innerHTML='密码已重置，其它设备的登录也已失效。<br><a href="/">返回登录</a>';
     }else{btn.disabled=false;m.className='msg err';m.textContent=j.msg||'重置失败，请重试';}
   })
   .catch(function(){btn.disabled=false;m.className='msg err';m.textContent='网络错误，请重试';});
});
</script></body></html>`
