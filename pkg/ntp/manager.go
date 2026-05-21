package ntp

import (
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"time"
)

type commandSpec struct {
	name string
	args []string
}

// Manager manages NTP service

type Manager struct {
    mu           sync.RWMutex
    ntpServers   []string
    applyCommand string
}

// NewManager creates a new NTP manager
func NewManager(ntpServers []string, applyCommand string) *Manager {
	return &Manager{
		ntpServers:   ntpServers,
		applyCommand: applyCommand,
	}
}

// Apply runs the user-specified NTP apply command (if set)
// This should be called when a new cluster decision is applied.
func (m *Manager) Apply(mode string, orderID uint64, manualTime *time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.applyCommand == "" {
		return nil // No-op if not configured
	}

	// Prepare environment variables for the command
	env := []string{
		fmt.Sprintf("TIMECTL_MODE=%s", mode),
		fmt.Sprintf("TIMECTL_ORDER_ID=%d", orderID),
	}
	if manualTime != nil {
		env = append(env, fmt.Sprintf("TIMECTL_MANUAL_TIME=%s", manualTime.Format(time.RFC3339)))
	}

	// Split command for exec.Command
	parts := strings.Fields(m.applyCommand)
	if len(parts) == 0 {
		return fmt.Errorf("invalid NTP apply command")
	}
	cmd := exec.Command(parts[0], parts[1:]...)
	cmd.Env = append(cmd.Env, env...)

	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to run NTP apply command: %w\noutput: %s", err, string(output))
	}
	return nil
}


// Stop is now a no-op (legacy)
func (m *Manager) Stop() error {
	return nil
}

// SetNTPServers configures NTP servers
func (m *Manager) SetNTPServers(servers []string) error {
	m.mu.Lock()
	m.ntpServers = servers
	m.mu.Unlock()
	return nil
}

// applyNTPConfiguration applies NTP server configuration
func (m *Manager) applyNTPConfiguration() error {
	return nil
}

// SyncTime synchronizes with NTP servers
func (m *Manager) SyncTime() error {
	return nil
}

// SetSystemTime sets the system time manually
func (m *Manager) SetSystemTime(t time.Time) error {
	return nil
}

// GetSystemTime gets the current system time
func (m *Manager) GetSystemTime() (time.Time, error) {
	return time.Now(), nil
}

// CheckNTPStatus checks the status of NTP service
func (m *Manager) CheckNTPStatus() (bool, error) {
	return false, nil
}

func runFirstSuccessful(commands []commandSpec) error {
	var lastErr error
	for _, c := range commands {
		output, err := exec.Command(c.name, c.args...).CombinedOutput()
		if err == nil {
			return nil
		}
		lastErr = fmt.Errorf("%s %s: %w (output: %s)", c.name, strings.Join(c.args, " "), err, strings.TrimSpace(string(output)))
	}
	if lastErr == nil {
		return fmt.Errorf("no command candidates provided")
	}
	return lastErr
}

// IsRunning returns whether NTP service is running
func (m *Manager) IsRunning() bool {
	return false
}

// GetNTPServers returns the configured NTP servers
func (m *Manager) GetNTPServers() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	servers := make([]string, len(m.ntpServers))
	copy(servers, m.ntpServers)
	return servers
}

// QueryNTPOffset queries the time offset from NTP servers
func (m *Manager) QueryNTPOffset() (time.Duration, error) {
	return 0, nil
}

// SetNTPMode sets whether to use NTP synchronization
func (m *Manager) SetNTPMode(enabled bool) error {
	return nil
}

// EnableNTPSync enables NTP synchronization
func (m *Manager) EnableNTPSync() error {
	return nil
}

// DisableNTPSync disables NTP synchronization
func (m *Manager) DisableNTPSync() error {
	return nil
}
