package mayor

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/steveyegge/gastown/internal/workspace"
)

const (
	acpPidFileName = "mayor-acp.pid"
)

var (
	ErrCleanupVetoed = fmt.Errorf("cleanup vetoed: ACP session is active")
)

func acpPidFilePath(townRoot string) string {
	return filepath.Join(townRoot, "mayor", acpPidFileName)
}

func WriteACPPid(townRoot string) error {
	mayorDir := filepath.Join(townRoot, "mayor")
	if err := os.MkdirAll(mayorDir, 0755); err != nil {
		return fmt.Errorf("creating mayor directory: %w", err)
	}

	pidPath := acpPidFilePath(townRoot)
	pid := os.Getpid()
	if err := os.WriteFile(pidPath, []byte(strconv.Itoa(pid)), 0644); err != nil {
		return fmt.Errorf("writing ACP PID file: %w", err)
	}
	return nil
}

func RemoveACPPid(townRoot string) error {
	pidPath := acpPidFilePath(townRoot)
	if _, err := os.Stat(pidPath); os.IsNotExist(err) {
		return nil
	}
	return os.Remove(pidPath)
}

func IsACPActive(townRoot string) bool {
	pidPath := acpPidFilePath(townRoot)
	data, err := os.ReadFile(pidPath)
	if err != nil {
		return false
	}

	pidStr := strings.TrimSpace(string(data))
	pid, err := strconv.Atoi(pidStr)
	if err != nil {
		return false
	}

	process, err := os.FindProcess(pid)
	if err != nil {
		return false
	}

	err = process.Signal(syscall.Signal(0))
	if err != nil {
		_ = os.Remove(pidPath)
		return false
	}

	return true
}

func IsACPActiveInWorkDir(workDir string) bool {
	townRoot, err := workspace.Find(workDir)
	if err != nil || townRoot == "" {
		return false
	}
	return IsACPActive(townRoot)
}

type CleanupVetoChecker struct {
	townRoot string
}

func NewCleanupVetoChecker(townRoot string) *CleanupVetoChecker {
	return &CleanupVetoChecker{townRoot: townRoot}
}

func NewCleanupVetoCheckerFromWorkDir(workDir string) (*CleanupVetoChecker, error) {
	townRoot, err := workspace.Find(workDir)
	if err != nil {
		return nil, fmt.Errorf("finding town root: %w", err)
	}
	if townRoot == "" {
		return nil, fmt.Errorf("not in a Gas Town workspace")
	}
	return NewCleanupVetoChecker(townRoot), nil
}

func (c *CleanupVetoChecker) ShouldVetoCleanup() (bool, string) {
	if IsACPActive(c.townRoot) {
		return true, "ACP session is active - Mayor may be reviewing worker diffs"
	}
	return false, ""
}

func (c *CleanupVetoChecker) VetoIfActive() error {
	if vetoed, reason := c.ShouldVetoCleanup(); vetoed {
		return fmt.Errorf("%w: %s", ErrCleanupVetoed, reason)
	}
	return nil
}

func (c *CleanupVetoChecker) GetACPExpiry() (time.Time, bool) {
	pidPath := acpPidFilePath(c.townRoot)
	info, err := os.Stat(pidPath)
	if err != nil {
		return time.Time{}, false
	}
	return info.ModTime(), true
}

func (c *CleanupVetoChecker) CleanupStaleACP() error {
	pidPath := acpPidFilePath(c.townRoot)
	if _, err := os.Stat(pidPath); os.IsNotExist(err) {
		return nil
	}

	if IsACPActive(c.townRoot) {
		return nil
	}

	return RemoveACPPid(c.townRoot)
}
