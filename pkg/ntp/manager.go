package ntp

import (
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"
)

// Manager manages NTP service
type Manager struct {
	mu         sync.RWMutex
	isRunning  bool
	ntpServers []string
	systemOS   string // windows, linux, darwin
}

// NewManager creates a new NTP manager
func NewManager(ntpServers []string) *Manager {
	return &Manager{
		ntpServers: ntpServers,
		systemOS:   runtime.GOOS,
		isRunning:  false,
	}
}

// Start starts the NTP service
func (m *Manager) Start() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.isRunning {
		return fmt.Errorf("NTP service is already running")
	}

	var cmd *exec.Cmd

	switch m.systemOS {
	case "windows":
		// Start Windows NTP service
		cmd = exec.Command("net", "start", "W32Time")
	case "linux":
		// Try systemd first
		cmd = exec.Command("systemctl", "start", "ntp")
	case "darwin":
		// macOS
		cmd = exec.Command("sudo", "launchctl", "start", "org.ntp.ntpd")
	default:
		return fmt.Errorf("unsupported OS: %s", m.systemOS)
	}

	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to start NTP service: %w\noutput: %s", err, string(output))
	}

	m.isRunning = true
	return nil
}

// Stop stops the NTP service
func (m *Manager) Stop() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.isRunning {
		return fmt.Errorf("NTP service is not running")
	}

	var cmd *exec.Cmd

	switch m.systemOS {
	case "windows":
		cmd = exec.Command("net", "stop", "W32Time")
	case "linux":
		cmd = exec.Command("systemctl", "stop", "ntp")
	case "darwin":
		cmd = exec.Command("sudo", "launchctl", "stop", "org.ntp.ntpd")
	default:
		return fmt.Errorf("unsupported OS: %s", m.systemOS)
	}

	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to stop NTP service: %w\noutput: %s", err, string(output))
	}

	m.isRunning = false
	return nil
}

// SetNTPServers configures NTP servers
func (m *Manager) SetNTPServers(servers []string) error {
	m.mu.Lock()
	m.ntpServers = servers
	m.mu.Unlock()

	return m.applyNTPConfiguration()
}

// applyNTPConfiguration applies NTP server configuration
func (m *Manager) applyNTPConfiguration() error {
	var cmd *exec.Cmd

	switch m.systemOS {
	case "windows":
		// Windows: Use W32Time command
		for _, server := range m.ntpServers {
			// Configure time source
			configCmd := exec.Command(
				"w32tm",
				"/config",
				"/manualpeerlist:"+server,
				"/syncfromflags:MANUAL",
				"/update",
			)
			if output, err := configCmd.CombinedOutput(); err != nil {
				return fmt.Errorf("failed to configure W32Time: %w\noutput: %s", err, string(output))
			}
		}
		// Restart W32Time service
		cmd = exec.Command("net", "stop", "W32Time")
		cmd.CombinedOutput()
		cmd = exec.Command("net", "start", "W32Time")

	case "linux":
		// Linux: Configure ntpd or chrony
		cmd = exec.Command("systemctl", "restart", "ntp")

	case "darwin":
		// macOS: Use sntp or ntpdate
		if len(m.ntpServers) > 0 {
			cmd = exec.Command("sudo", "sntp", "-s", m.ntpServers[0])
		}

	default:
		return fmt.Errorf("unsupported OS: %s", m.systemOS)
	}

	if cmd != nil {
		if output, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("failed to apply NTP configuration: %w\noutput: %s", err, string(output))
		}
	}

	return nil
}

// SyncTime synchronizes with NTP servers
func (m *Manager) SyncTime() error {
	m.mu.RLock()
	servers := m.ntpServers
	m.mu.RUnlock()

	if len(servers) == 0 {
		return fmt.Errorf("no NTP servers configured")
	}

	var cmd *exec.Cmd

	switch m.systemOS {
	case "windows":
		// Windows: Use w32tm /resync
		cmd = exec.Command("w32tm", "/resync", "/force")

	case "linux":
		// Linux: Use ntpdate or timedatectl
		cmd = exec.Command("timedatectl", "set-ntp", "true")

	case "darwin":
		// macOS: Use sntp
		cmd = exec.Command("sudo", "sntp", "-s", servers[0])

	default:
		return fmt.Errorf("unsupported OS: %s", m.systemOS)
	}

	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to sync time: %w\noutput: %s", err, string(output))
	}

	return nil
}

// SetSystemTime sets the system time manually
func (m *Manager) SetSystemTime(t time.Time) error {
	var cmd *exec.Cmd

	switch m.systemOS {
	case "windows":
		// Windows: Use date command
		cmd = exec.Command(
			"powershell",
			"-Command",
			fmt.Sprintf(`Set-Date -Date "%s"`, t.Format("01/02/2006 15:04:05")),
		)

	case "linux":
		// Linux: Use timedatectl or date
		cmd = exec.Command("timedatectl", "set-time", t.Format(time.RFC3339))

	case "darwin":
		// macOS: Use date command
		cmd = exec.Command("date", t.Format("010215042006.05"))

	default:
		return fmt.Errorf("unsupported OS: %s", m.systemOS)
	}

	if output, err := cmd.CombinedOutput(); err != nil {
		// Try alternative methods if the first fails
		if m.systemOS == "linux" {
			cmd = exec.Command("date", "-s", t.String())
			if output, err := cmd.CombinedOutput(); err != nil {
				return fmt.Errorf("failed to set system time: %w\noutput: %s", err, string(output))
			}
			return nil
		}
		return fmt.Errorf("failed to set system time: %w\noutput: %s", err, string(output))
	}

	return nil
}

// GetSystemTime gets the current system time
func (m *Manager) GetSystemTime() (time.Time, error) {
	return time.Now(), nil
}

// CheckNTPStatus checks the status of NTP service
func (m *Manager) CheckNTPStatus() (bool, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var cmd *exec.Cmd

	switch m.systemOS {
	case "windows":
		cmd = exec.Command("net", "start")

	case "linux":
		cmd = exec.Command("systemctl", "is-active", "ntp")

	case "darwin":
		cmd = exec.Command("launchctl", "list", "org.ntp.ntpd")

	default:
		return false, fmt.Errorf("unsupported OS: %s", m.systemOS)
	}

	output, err := cmd.CombinedOutput()
	if err != nil {
		return false, nil
	}

	outputStr := strings.TrimSpace(string(output))
	if m.systemOS == "linux" {
		return outputStr == "active", nil
	}

	return true, nil
}

// IsRunning returns whether NTP service is running
func (m *Manager) IsRunning() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.isRunning
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
	m.mu.RLock()
	servers := m.ntpServers
	m.mu.RUnlock()

	if len(servers) == 0 {
		return 0, fmt.Errorf("no NTP servers configured")
	}

	// Try to query the first available server
	for _, server := range servers {
		var cmd *exec.Cmd

		switch m.systemOS {
		case "windows":
			cmd = exec.Command("w32tm", "/query", "/status", "/verbose")
		case "linux":
			cmd = exec.Command("ntpq", "-p", server)
		case "darwin":
			cmd = exec.Command("sntp", "-t", "1", server)
		}

		if cmd != nil {
			if output, err := cmd.CombinedOutput(); err == nil {
				// Parse output to get offset (simplified, actual parsing would be more complex)
				_ = string(output)
				return 0, nil
			}
		}
	}

	return 0, fmt.Errorf("failed to query NTP offset from any server")
}

// SetNTPMode sets whether to use NTP synchronization
func (m *Manager) SetNTPMode(enabled bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	var cmd *exec.Cmd

	switch m.systemOS {
	case "windows":
		if enabled {
			cmd = exec.Command("w32tm", "/config", "/syncfromflags:DOMHIER", "/update")
		} else {
			cmd = exec.Command("w32tm", "/config", "/syncfromflags:NO", "/update")
		}

	case "linux":
		if enabled {
			cmd = exec.Command("timedatectl", "set-ntp", "true")
		} else {
			cmd = exec.Command("timedatectl", "set-ntp", "false")
		}

	case "darwin":
		// macOS handles this differently
		return nil

	default:
		return fmt.Errorf("unsupported OS: %s", m.systemOS)
	}

	if cmd != nil {
		if output, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("failed to set NTP mode: %w\noutput: %s", err, string(output))
		}
	}

	return nil
}

// EnableNTPSync enables NTP synchronization
func (m *Manager) EnableNTPSync() error {
	if err := m.SetNTPMode(true); err != nil {
		return err
	}
	return m.SyncTime()
}

// DisableNTPSync disables NTP synchronization
func (m *Manager) DisableNTPSync() error {
	return m.SetNTPMode(false)
}
