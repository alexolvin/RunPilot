// Профиль Grok: JS-экстракторы (ТЗ Relay §9). R6: placeholder — реальные
// селекторы/экстракторы по фикстурам (после логина оператора в профиле сессии).
window.__rp_userMessages = function () {
  return 0;
};

// Индекс последнего сообщения оркестратора после userIndex (или -1).
window.__rp_lastAssistantAfter = function (userIndex) {
  return -1;
};

// Блоки сообщения по индексу: [{kind: "text"|"code", text}].
window.__rp_blocks = function (messageIndex) {
  return [];
};
