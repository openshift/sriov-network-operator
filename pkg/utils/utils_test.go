package utils

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"golang.org/x/sys/unix"
)

func TestWriteFileWithTimeout_Success(t *testing.T) {
	tmpFile := filepath.Join(t.TempDir(), "test")
	data := []byte("hello")

	err := WriteFileWithTimeout(tmpFile, data, 0644, 5*time.Second)
	assert.NoError(t, err, "expected no error writing file")

	got, err := os.ReadFile(tmpFile)
	assert.NoError(t, err, "expected no error reading file")
	assert.Equal(t, data, got, "file contents do not match expected")
}

func TestWriteFileWithTimeout_WriteError(t *testing.T) {
	// Writing to a path that doesn't exist should return the underlying error, not a timeout.
	err := WriteFileWithTimeout("/nonexistent/dir/file", []byte("x"), 0644, 5*time.Second)
	assert.Error(t, err, "expected error for nonexistent path")
}

func TestWriteFileWithTimeout_Timeout(t *testing.T) {
	// A named pipe (FIFO) blocks on open/write until a reader is connected,
	// which makes it a reliable way to simulate a blocking write.
	fifoPath := filepath.Join(t.TempDir(), "fifo")
	assert.NoError(t, unix.Mkfifo(fifoPath, 0600), "failed to create FIFO")
	defer os.Remove(fifoPath)

	start := time.Now()
	err := WriteFileWithTimeout(fifoPath, []byte("data"), 0644, 100*time.Millisecond)
	elapsed := time.Since(start)

	assert.EqualError(t, err, "timeout writing to file "+fifoPath+" after 100ms", "expected timeout error")
	// The elapsed time should be close to the specified timeout, indicating that the function properly timed out.
	assert.GreaterOrEqual(t, elapsed, 100*time.Millisecond, "function returned too quickly, timeout may not have triggered")
	assert.Less(t, elapsed, 5*time.Second, "function took too long, timeout did not fire in time")
}
