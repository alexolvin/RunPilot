package fakeorch

// pageLogin — страница входа (#login-form).
const pageLogin = `<!doctype html>
<html lang="ru"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>fakeorch · вход</title>
<style>
 body{font-family:system-ui,sans-serif;margin:0;background:#111;color:#eee}
 .wrap{max-width:360px;margin:12vh auto;padding:24px;border:1px solid #333;border-radius:12px}
 h1{font-size:18px}
 input{width:100%;box-sizing:border-box;padding:10px;margin:8px 0;border:1px solid #444;border-radius:8px;background:#1a1a1a;color:#eee}
 button{width:100%;padding:10px;border:0;border-radius:8px;background:#2dd4bf;color:#04222b;font-weight:600;cursor:pointer}
</style></head>
<body>
<div class="wrap">
 <h1>Вход в fakeorch</h1>
 <form id="login-form">
  <input name="token" value="op" autocomplete="off">
  <button type="submit">Войти</button>
 </form>
</div>
<script>
document.getElementById('login-form').addEventListener('submit', async (e) => {
  e.preventDefault();
  const fd = new FormData(e.target);
  await fetch('/_login', { method: 'POST', body: fd });
  location.reload();
});
</script>
</body></html>`

// pageChat — страница чата: лента, композер, файлы, кнопка.
// __ID__ заменяется на ключ чата.
const pageChat = `<!doctype html>
<html lang="ru"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>fakeorch · chat __ID__</title>
<style>
 body{font-family:system-ui,sans-serif;margin:0;background:#111;color:#eee}
 #feed{padding:16px;display:flex;flex-direction:column;gap:10px}
 .msg{padding:10px 12px;border-radius:10px;max-width:80%;white-space:pre-wrap}
 .msg.user{align-self:flex-end;background:#1f6f63}
 .msg.assistant{align-self:flex-start;background:#222}
 .block{margin:4px 0}
 .block code{display:block;background:#000;padding:8px;border-radius:6px;white-space:pre-wrap;font-family:ui-monospace,monospace}
 .generating{align-self:flex-start;color:#2dd4bf;font-size:13px}
 .bar{display:flex;gap:8px;padding:12px;border-top:1px solid #333;align-items:center}
 #composer{flex:1;box-sizing:border-box;padding:10px;border:1px solid #444;border-radius:8px;background:#1a1a1a;color:#eee;min-height:44px}
 #send{padding:10px 16px;border:0;border-radius:8px;background:#2dd4bf;color:#04222b;font-weight:600;cursor:pointer}
 #files{display:none}
 .chip{display:inline-block;background:#333;border-radius:12px;padding:2px 10px;font-size:12px;margin:0 4px}
 .chip.uploading{opacity:.5}
 #chips{padding:0 12px 4px}
</style></head>
<body>
<div id="feed"></div>
<div id="chips"></div>
<div class="bar">
 <textarea id="composer"></textarea>
 <input type="file" id="files" multiple>
 <button id="send">Отправить</button>
</div>
<script>
const CHAT = '__ID__';
let CFG = { answer_delay_ms: 300, file_delay_ms: 100 };
async function loadCfg() {
  const r = await fetch('/_config');
  CFG = await r.json();
}
function renderMsg(role, blocks) {
  const feed = document.getElementById('feed');
  const div = document.createElement('div');
  div.className = 'msg ' + role;
  blocks.forEach(b => {
    const blk = document.createElement('div');
    blk.className = 'block';
    if (b.kind === 'code') {
      const c = document.createElement('code');
      c.textContent = b.text;
      blk.appendChild(c);
    } else {
      blk.textContent = b.text;
    }
    div.appendChild(blk);
  });
  feed.appendChild(div);
  feed.scrollTop = feed.scrollHeight;
}
function renderFeed() {
  fetch('/chat/' + CHAT).then(r => r.json()).then(msgs => {
    (msgs || []).forEach(m => renderMsg(m.role, m.blocks));
  });
}
document.getElementById('files').addEventListener('change', () => {
  const chips = document.getElementById('chips');
  chips.innerHTML = '';
  const fl = document.getElementById('files').files;
  for (let i = 0; i < fl.length; i++) {
    const c = document.createElement('span');
    c.className = 'chip uploading';
    c.textContent = fl[i].name;
    chips.appendChild(c);
  }
  setTimeout(() => {
    chips.querySelectorAll('.chip.uploading').forEach(c => c.classList.remove('uploading'));
  }, CFG.file_delay_ms);
});
document.getElementById('send').addEventListener('click', async () => {
  const comp = document.getElementById('composer');
  const text = comp.value;
  const fd = new FormData();
  fd.append('chat', CHAT);
  fd.append('text', text);
  const fl = document.getElementById('files').files;
  for (let i = 0; i < fl.length; i++) fd.append('files', fl[i]);
  const r = await fetch('/send', { method: 'POST', body: fd });
  const out = await r.json();
  if (out.lost) return; // нажатие потеряно: сообщение не появляется
  renderMsg('user', [{ kind: 'text', text: text }]);
  comp.value = '';
  const gen = document.createElement('div');
  gen.className = 'generating';
  gen.textContent = '…';
  document.getElementById('feed').appendChild(gen);
  setTimeout(() => {
    gen.remove();
    renderMsg('assistant', out.answer.blocks);
  }, CFG.answer_delay_ms);
});
loadCfg().then(renderFeed);
</script>
</body></html>`
