package api

// C6 / U3: сменилась версия Qwen Code. Узел выполняет version_cmd при каждом
// запуске и перезапуске кодера; версия без фикстур → сессия помечается
// untested + [WARN] «Qwen Code V не проверен» (edge-trigger: один раз на
// версию). Процедура добавления фикстур — docs/HANDOFF.md.

import (
	"fmt"

	"runpilot/internal/proto"
)

// testedQwenVersions — версии Qwen Code, на которые сняты фикстуры (раздел 7
// ТЗ C6). Зеркалит testdata/fixtures/qwen/<version>/; при добавлении фикстур
// версия дописывается сюда (процедура — docs/HANDOFF.md).
var testedQwenVersions = map[string]bool{
	"0.24.4": true,
	"0.24.5": true,
}

// qwenUntested — версия кодера есть и на неё нет фикстур (C6).
func qwenUntested(version string) bool {
	return version != "" && !testedQwenVersions[version]
}

// checkAgentVersion — C6: версия кодера без фикстур → [WARN] «не проверен»
// (edge-trigger: один раз на версию).
func (s *Server) checkAgentVersion(sid, version string) {
	if !qwenUntested(version) {
		return
	}
	s.warnedVerMu.Lock()
	if s.warnedVer[version] {
		s.warnedVerMu.Unlock()
		return
	}
	s.warnedVer[version] = true
	s.warnedVerMu.Unlock()
	s.notify(fmt.Sprintf("runpilot: [WARN] Qwen Code %s не проверен (нет фикстур); сессия %s помечена untested", version, sid))
}

// syncAgentVersion — C6: узел сообщает версию кодера в снимке панели
// (version_cmd при запуске/перезапуске). Если версия сменилась — обновляем
// сессию и проверяем на фикстуры.
func (s *Server) syncAgentVersion(p proto.Pane) {
	if p.SID == "" || p.AgentVersion == "" {
		return
	}
	rec, err := s.store.GetSession(p.SID)
	if err != nil {
		return
	}
	if rec.AgentVersion == p.AgentVersion {
		return
	}
	if err := s.store.UpdateSessionAgentVersion(p.SID, p.AgentVersion); err != nil {
		s.log.Warn("api: runpilot: версия кодера: " + err.Error())
		return
	}
	s.checkAgentVersion(p.SID, p.AgentVersion)
}
