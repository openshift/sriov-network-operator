package utils

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"golang.org/x/sys/unix"
)

func TestValidateOvsConfig(t *testing.T) {
	tests := []struct {
		name      string
		config    map[string]string
		expectErr bool
	}{
		{
			name:      "nil map",
			config:    nil,
			expectErr: false,
		},
		{
			name:      "empty map",
			config:    map[string]string{},
			expectErr: false,
		},
		{
			name:      "valid keys and values",
			config:    map[string]string{"hw-offload": "true", "tc_policy": "none"},
			expectErr: false,
		},
		{
			name:      "value with special characters other than single quote",
			config:    map[string]string{"key": `value with "double" quotes and $pecial`},
			expectErr: false,
		},
		{
			name:      "key with dot",
			config:    map[string]string{"invalid.key": "value"},
			expectErr: true,
		},
		{
			name:      "key with space",
			config:    map[string]string{"invalid key": "value"},
			expectErr: true,
		},
		{
			name:      "key with shell special character",
			config:    map[string]string{"key$name": "value"},
			expectErr: true,
		},
		{
			name:      "empty key",
			config:    map[string]string{"": "value"},
			expectErr: true,
		},
		{
			name:      "value with single quote",
			config:    map[string]string{"key": "val'ue"},
			expectErr: true,
		},
		{
			name:      "value is only a single quote",
			config:    map[string]string{"key": "'"},
			expectErr: true,
		},
		{
			name:      "second key invalid",
			config:    map[string]string{"good-key": "value", "bad key": "value"},
			expectErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateOvsConfig(tt.config)
			if tt.expectErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestRenderOtherOvsConfigOption(t *testing.T) {
	tests := []struct {
		name            string
		config          map[string]string
		wantExternalIds string
		wantOtherConfig string
		expectErr       bool
	}{
		{
			name:            "nil map",
			config:          nil,
			wantExternalIds: "",
			wantOtherConfig: "",
		},
		{
			name:            "empty map",
			config:          map[string]string{},
			wantExternalIds: "",
			wantOtherConfig: "",
		},
		{
			name:            "single entry",
			config:          map[string]string{"hw-offload": "true"},
			wantExternalIds: "hw-offload",
			wantOtherConfig: `other_config:hw-offload="true" `,
		},
		{
			name:   "multiple entries are sorted by key",
			config: map[string]string{"tc_policy": "none", "hw-offload": "true"},
			// keys sorted: hw-offload < tc_policy
			wantExternalIds: "hw-offload tc_policy",
			wantOtherConfig: `other_config:hw-offload="true" other_config:tc_policy="none" `,
		},
		{
			name:            "value with double quotes is escaped by %q",
			config:          map[string]string{"key": `val"ue`},
			wantExternalIds: "key",
			wantOtherConfig: `other_config:key="val\"ue" `,
		},
		{
			name:            "value with dollar sign is kept as-is",
			config:          map[string]string{"key": "$value"},
			wantExternalIds: "key",
			wantOtherConfig: `other_config:key="$value" `,
		},
		{
			name:      "invalid key with dot returns error",
			config:    map[string]string{"bad.key": "value"},
			expectErr: true,
		},
		{
			name:      "value with single quote returns error",
			config:    map[string]string{"key": "val'ue"},
			expectErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			externalIds, otherConfig, err := RenderOtherOvsConfigOption(tt.config)
			if tt.expectErr {
				assert.Error(t, err)
				return
			}
			assert.NoError(t, err)
			assert.Equal(t, tt.wantExternalIds, externalIds)
			assert.Equal(t, tt.wantOtherConfig, otherConfig)
		})
	}
}

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
