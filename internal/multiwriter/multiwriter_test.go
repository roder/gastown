package multiwriter

import (
	"bytes"
	"io"
	"sync"
	"testing"
)

func TestNew(t *testing.T) {
	var buf1, buf2 bytes.Buffer
	mw := New(&buf1, &buf2)

	if mw == nil {
		t.Fatal("New returned nil")
	}
	if mw.Len() != 2 {
		t.Errorf("Len() = %d, want 2", mw.Len())
	}
}

func TestNewWithNil(t *testing.T) {
	var buf bytes.Buffer
	mw := New(nil, &buf, nil)

	if mw.Len() != 1 {
		t.Errorf("Len() = %d, want 1 (nil writers should be skipped)", mw.Len())
	}
}

func TestWrite(t *testing.T) {
	var buf1, buf2 bytes.Buffer
	mw := New(&buf1, &buf2)

	data := []byte("hello")
	n, err := mw.Write(data)
	if err != nil {
		t.Fatalf("Write failed: %v", err)
	}
	if n != len(data) {
		t.Errorf("Write returned %d, want %d", n, len(data))
	}

	if buf1.String() != "hello" {
		t.Errorf("buf1 = %q, want %q", buf1.String(), "hello")
	}
	if buf2.String() != "hello" {
		t.Errorf("buf2 = %q, want %q", buf2.String(), "hello")
	}
}

func TestWriteNoWriters(t *testing.T) {
	mw := New()

	data := []byte("hello")
	n, err := mw.Write(data)
	if err != nil {
		t.Fatalf("Write failed: %v", err)
	}
	if n != len(data) {
		t.Errorf("Write returned %d, want %d", n, len(data))
	}
}

func TestWriteError(t *testing.T) {
	errWriter := &errWriter{}
	mw := New(errWriter)

	_, err := mw.Write([]byte("hello"))
	if err != errTest {
		t.Errorf("Write error = %v, want %v", err, errTest)
	}
}

var errTest = io.ErrClosedPipe

type errWriter struct{}

func (errWriter) Write(p []byte) (n int, err error) {
	return 0, errTest
}

func TestWriteShortWrite(t *testing.T) {
	shortWriter := &shortWriter{}
	mw := New(shortWriter)

	_, err := mw.Write([]byte("hello"))
	if err != io.ErrShortWrite {
		t.Errorf("Write error = %v, want %v", err, io.ErrShortWrite)
	}
}

type shortWriter struct{}

func (shortWriter) Write(p []byte) (n int, err error) {
	return len(p) - 1, nil
}

func TestAdd(t *testing.T) {
	var buf1, buf2 bytes.Buffer
	mw := New(&buf1)

	mw.Add(&buf2)
	if mw.Len() != 2 {
		t.Errorf("Len() = %d, want 2", mw.Len())
	}

	mw.Write([]byte("test"))
	if buf2.String() != "test" {
		t.Errorf("buf2 = %q, want %q", buf2.String(), "test")
	}
}

func TestAddNil(t *testing.T) {
	mw := New()
	mw.Add(nil)

	if mw.Len() != 0 {
		t.Errorf("Len() = %d, want 0 (nil should not be added)", mw.Len())
	}
}

func TestRemove(t *testing.T) {
	var buf1, buf2 bytes.Buffer
	mw := New(&buf1, &buf2)

	mw.Remove(&buf1)
	if mw.Len() != 1 {
		t.Errorf("Len() = %d, want 1", mw.Len())
	}

	mw.Write([]byte("test"))
	if buf1.String() != "" {
		t.Errorf("buf1 = %q, want empty (removed)", buf1.String())
	}
	if buf2.String() != "test" {
		t.Errorf("buf2 = %q, want %q", buf2.String(), "test")
	}
}

func TestRemoveNotFound(t *testing.T) {
	var buf bytes.Buffer
	mw := New(&buf)

	var otherBuf bytes.Buffer
	mw.Remove(&otherBuf)

	if mw.Len() != 1 {
		t.Errorf("Len() = %d, want 1 (no-op for non-existent)", mw.Len())
	}
}

func TestWriters(t *testing.T) {
	var buf1, buf2 bytes.Buffer
	mw := New(&buf1, &buf2)

	writers := mw.Writers()
	if len(writers) != 2 {
		t.Errorf("Writers() returned %d writers, want 2", len(writers))
	}

	writers[0] = nil
	if mw.Len() != 2 {
		t.Errorf("modifying returned slice affected internal state")
	}
}

func TestConcurrentWrite(t *testing.T) {
	var buf bytes.Buffer
	var mu sync.Mutex
	writer := writerFunc(func(p []byte) (int, error) {
		mu.Lock()
		defer mu.Unlock()
		return buf.Write(p)
	})
	mw := New(writer)

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			mw.Write([]byte("x"))
		}()
	}
	wg.Wait()

	if buf.Len() != 100 {
		t.Errorf("buf.Len() = %d, want 100", buf.Len())
	}
}

type writerFunc func(p []byte) (int, error)

func (w writerFunc) Write(p []byte) (int, error) {
	return w(p)
}

func TestConcurrentAddRemove(t *testing.T) {
	mw := New()

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			var buf bytes.Buffer
			mw.Add(&buf)
		}()
		go func() {
			defer wg.Done()
			var buf bytes.Buffer
			mw.Remove(&buf)
		}()
	}
	wg.Wait()
}

func TestGlobalWriter(t *testing.T) {
	var buf1, buf2 bytes.Buffer

	SetGlobal(New(&buf1))
	AddGlobal(&buf2)

	gw := GetGlobal()
	if gw == nil {
		t.Fatal("GetGlobal returned nil")
	}

	gw.Write([]byte("test"))

	if buf1.String() != "test" {
		t.Errorf("buf1 = %q, want %q", buf1.String(), "test")
	}
	if buf2.String() != "test" {
		t.Errorf("buf2 = %q, want %q", buf2.String(), "test")
	}

	RemoveGlobal(&buf2)
	if gw.Len() != 1 {
		t.Errorf("Len() = %d, want 1", gw.Len())
	}

	SetGlobal(nil)
	if GetGlobal() != nil {
		t.Error("GetGlobal should return nil after SetGlobal(nil)")
	}
}

func TestStdoutMux(t *testing.T) {
	mux := NewStdoutMux()
	if mux == nil {
		t.Fatal("NewStdoutMux returned nil")
	}

	var customBuf bytes.Buffer
	mux.AddWriter(&customBuf)

	writer := mux.Writer()
	if writer == nil {
		t.Fatal("Writer() returned nil")
	}

	writer.Write([]byte("test"))
	if customBuf.String() != "test" {
		t.Errorf("customBuf = %q, want %q", customBuf.String(), "test")
	}
}

func TestStdoutMuxInstallUninstall(t *testing.T) {
	mux := NewStdoutMux()

	writer := mux.Install()
	if writer == nil {
		t.Fatal("Install returned nil")
	}

	if !mux.installed {
		t.Error("installed should be true after Install")
	}

	mux.AddWriter(bytes.NewBuffer(nil))
	if mux.Writers()[0] == nil {
		t.Error("Writers() should include real stdout after Install")
	}

	mux.Uninstall()
}

func TestStdoutMuxDoubleInstall(t *testing.T) {
	mux := NewStdoutMux()

	mux.Install()
	installedWriters := mux.Len()

	mux.Install()
	if mux.Len() != installedWriters {
		t.Errorf("double Install changed writer count from %d to %d", installedWriters, mux.Len())
	}
}

func TestDefaultStdoutMux(t *testing.T) {
	mux1 := DefaultStdoutMux()
	mux2 := DefaultStdoutMux()

	if mux1 != mux2 {
		t.Error("DefaultStdoutMux should return the same instance")
	}
}

func TestSetAndGetOsStdout(t *testing.T) {
	var buf bytes.Buffer
	SetOsStdout(&buf)

	got := GetOsStdout()
	if got != &buf {
		t.Error("GetOsStdout should return the writer set by SetOsStdout")
	}

	SetOsStdout(nil)
}

func TestOsStdoutWriter(t *testing.T) {
	var buf bytes.Buffer
	cleanup := SetStdoutImplForTest(&buf)
	defer cleanup()

	writer := osStdoutWriter{}
	n, err := writer.Write([]byte("test"))
	if err != nil {
		t.Fatalf("Write failed: %v", err)
	}
	if n != 4 {
		t.Errorf("Write returned %d, want 4", n)
	}
	if buf.String() != "test" {
		t.Errorf("buf = %q, want %q", buf.String(), "test")
	}
}

func TestOsStdoutWriterNil(t *testing.T) {
	cleanup := SetStdoutImplForTest(nil)
	defer cleanup()

	writer := osStdoutWriter{}
	n, err := writer.Write([]byte("test"))
	if err != nil {
		t.Fatalf("Write failed: %v", err)
	}
	if n != 4 {
		t.Errorf("Write returned %d, want 4", n)
	}
}

func TestStdoutMuxInstallUsesRealStdout(t *testing.T) {
	var captured bytes.Buffer
	bufWriter := &bufWriter{&captured}
	cleanup := SetRealStdoutForTest(bufWriter)
	defer cleanup()

	mux := NewStdoutMux()
	mux.Install()

	var customBuf bytes.Buffer
	mux.AddWriter(&customBuf)

	writer := mux.Writer()
	writer.Write([]byte("hello"))

	if captured.String() != "hello" {
		t.Errorf("captured = %q, want %q", captured.String(), "hello")
	}
	if customBuf.String() != "hello" {
		t.Errorf("customBuf = %q, want %q", customBuf.String(), "hello")
	}

	mux.Uninstall()
}

type bufWriter struct {
	buf *bytes.Buffer
}

func (b *bufWriter) Write(p []byte) (int, error) {
	return b.buf.Write(p)
}
