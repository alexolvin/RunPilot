// Package logx — ротация лога по размеру.
package logx

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// RotateWriter — пишущий поток с ротацией по размеру:
// при превышении MaxBytes файл переименовывается в .1,
// открывается новый. Максимум MaxBackups бэкапов.
type RotateWriter struct {
	path       string
	maxBytes   int64
	maxBackups int
	f          *os.File
	size       int64
}

// NewRotateWriter — создаёт ротационный писатель.
// maxBytes — максимальный размер файла (байт); 0 — без ротации.
// maxBackups — сколько бэкапов хранить (.1, .2, ...); 0 — без бэкапов.
func NewRotateWriter(path string, maxBytes int64, maxBackups int) (*RotateWriter, error) {
	rw := &RotateWriter{
		path:       path,
		maxBytes:   maxBytes,
		maxBackups: maxBackups,
	}
	if err := rw.open(); err != nil {
		return nil, err
	}
	return rw, nil
}

func (rw *RotateWriter) open() error {
	if err := os.MkdirAll(filepath.Dir(rw.path), dirPerm); err != nil {
		return err
	}
	f, err := os.OpenFile(rw.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, filePerm)
	if err != nil {
		return err
	}
	rw.f = f
	info, err := f.Stat()
	if err == nil {
		rw.size = info.Size()
	}
	return nil
}

// Write — пишет данные; при превышении maxBytes — ротация.
func (rw *RotateWriter) Write(p []byte) (int, error) {
	if rw.maxBytes > 0 && rw.size+int64(len(p)) > rw.maxBytes {
		if err := rw.rotate(); err != nil {
			return 0, err
		}
	}
	n, err := rw.f.Write(p)
	rw.size += int64(n)
	return n, err
}

func (rw *RotateWriter) rotate() error {
	if err := rw.f.Close(); err != nil {
		return err
	}
	// Сдвигаем бэкапы: .N-1 → .N, ..., .1 → .2 (если есть).
	for i := rw.maxBackups - 1; i >= 1; i-- {
		src := fmt.Sprintf("%s.%d", rw.path, i)
		dst := fmt.Sprintf("%s.%d", rw.path, i+1)
		_ = os.Rename(src, dst)
	}
	if rw.maxBackups > 0 {
		last := fmt.Sprintf("%s.%d", rw.path, rw.maxBackups)
		_ = os.Remove(last)
		_ = os.Rename(rw.path, rw.path+".1")
	} else {
		_ = os.Remove(rw.path)
	}
	return rw.open()
}

// Close — закрывает файл.
func (rw *RotateWriter) Close() error {
	return rw.f.Close()
}

// Path — путь к лог-файлу.
func (rw *RotateWriter) Path() string { return rw.path }

// io.Closer — для defer.
var _ io.Closer = (*RotateWriter)(nil)
