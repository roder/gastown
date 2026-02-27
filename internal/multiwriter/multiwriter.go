package multiwriter

import (
	"io"
	"os"
	"sync"
)

type MultiWriter struct {
	mu      sync.RWMutex
	writers []io.Writer
}

func New(writers ...io.Writer) *MultiWriter {
	mw := &MultiWriter{
		writers: make([]io.Writer, 0, len(writers)),
	}
	for _, w := range writers {
		if w != nil {
			mw.writers = append(mw.writers, w)
		}
	}
	return mw
}

func (mw *MultiWriter) Write(p []byte) (n int, err error) {
	mw.mu.RLock()
	defer mw.mu.RUnlock()

	if len(mw.writers) == 0 {
		return len(p), nil
	}

	for _, w := range mw.writers {
		n, err = w.Write(p)
		if err != nil {
			return n, err
		}
		if n != len(p) {
			return n, io.ErrShortWrite
		}
	}
	return len(p), nil
}

func (mw *MultiWriter) Add(w io.Writer) {
	if w == nil {
		return
	}
	mw.mu.Lock()
	defer mw.mu.Unlock()
	mw.writers = append(mw.writers, w)
}

func (mw *MultiWriter) Remove(w io.Writer) {
	mw.mu.Lock()
	defer mw.mu.Unlock()
	for i, writer := range mw.writers {
		if writer == w {
			mw.writers = append(mw.writers[:i], mw.writers[i+1:]...)
			return
		}
	}
}

func (mw *MultiWriter) Writers() []io.Writer {
	mw.mu.RLock()
	defer mw.mu.RUnlock()
	result := make([]io.Writer, len(mw.writers))
	copy(result, mw.writers)
	return result
}

func (mw *MultiWriter) Len() int {
	mw.mu.RLock()
	defer mw.mu.RUnlock()
	return len(mw.writers)
}

var (
	globalMu     sync.RWMutex
	globalWriter *MultiWriter
)

func GetGlobal() *MultiWriter {
	globalMu.RLock()
	defer globalMu.RUnlock()
	return globalWriter
}

func SetGlobal(mw *MultiWriter) {
	globalMu.Lock()
	defer globalMu.Unlock()
	globalWriter = mw
}

func AddGlobal(w io.Writer) {
	globalMu.Lock()
	defer globalMu.Unlock()
	if globalWriter == nil {
		globalWriter = New()
	}
	globalWriter.Add(w)
}

func RemoveGlobal(w io.Writer) {
	globalMu.Lock()
	defer globalMu.Unlock()
	if globalWriter != nil {
		globalWriter.Remove(w)
	}
}

type StdoutMux struct {
	mu         sync.RWMutex
	multi      *MultiWriter
	realStdout io.Writer
	installed  bool
}

func NewStdoutMux() *StdoutMux {
	return &StdoutMux{
		multi: New(),
	}
}

func (m *StdoutMux) Install() io.Writer {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.installed {
		return m.multi
	}
	m.realStdout = getRealStdout()
	m.multi.Add(m.realStdout)
	m.installed = true
	return m.multi
}

func (m *StdoutMux) Uninstall() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.installed {
		return
	}
	m.multi.Remove(m.realStdout)
	m.installed = false
}

func (m *StdoutMux) Writer() io.Writer {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.multi
}

func (m *StdoutMux) AddWriter(w io.Writer) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	m.multi.Add(w)
}

func (m *StdoutMux) RemoveWriter(w io.Writer) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	m.multi.Remove(w)
}

func (m *StdoutMux) Writers() []io.Writer {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.multi.Writers()
}

func (m *StdoutMux) Len() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.multi.Len()
}

func getRealStdout() io.Writer {
	return realStdout
}

var realStdout io.Writer = osStdoutWriter{}

var stdoutImpl io.Writer

func init() {
	stdoutImpl = os.Stdout
}

type osStdoutWriter struct{}

func (osStdoutWriter) Write(p []byte) (n int, err error) {
	if stdoutImpl != nil {
		return stdoutImpl.Write(p)
	}
	return len(p), nil
}

var (
	osStdoutMu sync.RWMutex
	osStdout   io.Writer
)

func SetOsStdout(w io.Writer) {
	osStdoutMu.Lock()
	defer osStdoutMu.Unlock()
	osStdout = w
}

func GetOsStdout() io.Writer {
	osStdoutMu.RLock()
	defer osStdoutMu.RUnlock()
	return osStdout
}

func SetRealStdoutForTest(w io.Writer) func() {
	orig := realStdout
	realStdout = w
	return func() { realStdout = orig }
}

func SetStdoutImplForTest(w io.Writer) func() {
	orig := stdoutImpl
	stdoutImpl = w
	return func() { stdoutImpl = orig }
}

var defaultStdoutMux *StdoutMux
var defaultStdoutMuxOnce sync.Once

func DefaultStdoutMux() *StdoutMux {
	defaultStdoutMuxOnce.Do(func() {
		defaultStdoutMux = NewStdoutMux()
	})
	return defaultStdoutMux
}
