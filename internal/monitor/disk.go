package monitor

// K4: место на диске БД координатора. Проверка раз в monitor.disk_check_sec;
// свободное < monitor.disk_free_min_mb → [WARN] «Свободно N МБ». Чтение
// свободного места — statfs; в тестах подменяется SetDiskFreeSpace.

import (
	"strconv"
	"syscall"
	"time"
)

// statfsMB — свободное место на ФС пути в МБ (K4, default freeSpace).
func statfsMB(path string) (int64, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0, err
	}
	return int64(st.Bavail) * int64(st.Bsize) / (kibi * kibi), nil
}

// SetDiskPath — путь БД для проверки места (K4). По умолчанию freeSpace =
// statfs; первая проверка — сразу (при следующем Tick).
func (m *Monitor) SetDiskPath(path string) {
	m.diskMu.Lock()
	defer m.diskMu.Unlock()
	m.diskPath = path
	if m.freeSpace == nil {
		m.freeSpace = statfsMB
	}
	m.diskNext = m.clk.Now()
}

// SetDiskFreeSpace — подмена чтения свободного места (тесты).
func (m *Monitor) SetDiskFreeSpace(fn func(string) (int64, error)) {
	m.diskMu.Lock()
	defer m.diskMu.Unlock()
	m.freeSpace = fn
}

// CheckDisk — одна проверка места на диске БД (K4). Возвращает (freeMB,
// warned); warned=true — свободное ниже monitor.disk_free_min_mb ([WARN]).
func (m *Monitor) CheckDisk() (int64, bool) {
	m.diskMu.Lock()
	defer m.diskMu.Unlock()
	return m.diskCheckLocked()
}

// DiskFreeMB — последнее известное свободное место (вид/веб); ok=false, если
// проверка не включена.
func (m *Monitor) DiskFreeMB() (int64, bool) {
	m.diskMu.Lock()
	defer m.diskMu.Unlock()
	return m.lastDiskFree, m.diskPath != ""
}

// tickDisk — периодическая проверка (каждые monitor.disk_check_sec).
// Вызывается из Tick (m.mu здесь не держится).
func (m *Monitor) tickDisk() {
	m.diskMu.Lock()
	defer m.diskMu.Unlock()
	if m.freeSpace == nil || m.diskPath == "" {
		return
	}
	now := m.clk.Now()
	if now.Before(m.diskNext) {
		return
	}
	m.diskNext = now.Add(time.Duration(m.cfg.Monitor.DiskCheckSec) * time.Second)
	m.diskCheckLocked()
}

// diskCheckLocked — проверка под m.diskMu.
func (m *Monitor) diskCheckLocked() (int64, bool) {
	if m.freeSpace == nil || m.diskPath == "" {
		return 0, false
	}
	freeMB, err := m.freeSpace(m.diskPath)
	if err != nil {
		return 0, false
	}
	m.lastDiskFree = freeMB
	if int(freeMB) < m.cfg.Monitor.DiskFreeMinMB {
		m.log.Warn("runpilot: [WARN] мало места на диске БД: свободно " +
			strconv.FormatInt(freeMB, decimalBase) + " МБ (порог " +
			strconv.Itoa(m.cfg.Monitor.DiskFreeMinMB) + " МБ)")
		return freeMB, true
	}
	return freeMB, false
}
