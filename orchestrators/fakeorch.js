// Профиль fakeorch: JS-экстракторы (ТЗ Relay §9). Функции определяются на
// window при инжекции и вызываются go-rod (Eval) с простыми аргументами.
window.__rp_userMessages = function () {
  return document.querySelectorAll('.msg.user').length;
};

// Индекс последнего сообщения оркестратора после userIndex (или -1).
window.__rp_lastAssistantAfter = function (userIndex) {
  var msgs = Array.prototype.slice.call(document.querySelectorAll('.msg'));
  var last = -1;
  for (var i = userIndex + 1; i < msgs.length; i++) {
    if (msgs[i].classList.contains('assistant')) last = i;
  }
  return last;
};

// Блоки сообщения по индексу: [{kind: "text"|"code", text}] в порядке
// документа. Текст code — textContent элемента <code>, переводы строк
// сохраняются.
window.__rp_blocks = function (messageIndex) {
  var msgs = Array.prototype.slice.call(document.querySelectorAll('.msg'));
  var el = msgs[messageIndex];
  if (!el) return [];
  var out = [];
  el.querySelectorAll('.block').forEach(function (b) {
    var code = b.querySelector('code');
    if (code) out.push({ kind: 'code', text: code.textContent });
    else out.push({ kind: 'text', text: b.textContent });
  });
  return out;
};
