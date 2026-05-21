package ntp

import (
	"fmt"
	"os/exec"
	"runtime"
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

	return m.applyNTPConfiguration()
}

// applyNTPConfiguration applies NTP server configuration
func (m *Manager) applyNTPConfiguration() error {
	var cmd *exec.Cmd

	switch m.systemOS {
	case "windows":
		// Meinberg NTP: restart ntpd service to reload existing ntp.conf.
		if err := runFirstSuccessful([]commandSpec{{name: "net", args: []string{"stop", "NTP"}}, {name: "net", args: []string{"stop", "ntpd"}}, {name: "sc", args: []string{"stop", "NTP"}}, {name: "sc", args: []string{"stop", "ntpd"}}}); err != nil {
			// Ignore stop failures when service was not running.
		}
		if err := runFirstSuccessful([]commandSpec{{name: "net", args: []string{"start", "NTP"}}, {name: "net", args: []string{"start", "ntpd"}}, {name: "sc", args: []string{"start", "NTP"}}, {name: "sc", args: []string{"start", "ntpd"}}}); err != nil {
			return fmt.Errorf("failed to reload Meinberg NTP service: %w", err)
		}
        cmd = nil

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
		if len(servers) > 0 {
			cmd = exec.Command("ntpdate", "-u", servers[0])
		} else {
			cmd = exec.Command("ntpd", "-gq")
		}

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
		for _, c := range []commandSpec{{name: "sc", args: []string{"query", "NTP"}}, {name: "sc", args: []string{"query", "ntpd"}}} {
			out, err := exec.Command(c.name, c.args...).CombinedOutput()
			if err != nil {
				continue
			}
			outputStr := strings.ToUpper(string(out))
			if strings.Contains(outputStr, "RUNNING") {
				return true, nil
			}
		}
		return false, nil

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
