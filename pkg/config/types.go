package config

import (
	"time"
)

// TimeMode represents the operational mode of the time system
type TimeMode int

const (
	ModeAuto TimeMode = iota
	ModeManual
)

func (m TimeMode) String() string {
	switch m {
	case ModeAuto:
		return "AUTO"
	case ModeManual:
		return "MANUAL"
	default:
		return "UNKNOWN"
	}
}

// ServerConfig holds configuration for a time control server
type ServerConfig struct {
	NodeID           string
	RaftAddr         string        // Address for Raft communication (e.g., "localhost:5000")
	RPCAddr          string        // Address for gRPC communication (e.g., "localhost:5001")
	HTTPAddr         string        // Address for HTTP API (e.g., "localhost:8080")
	DataDir          string        // Directory for data storage
	ClusterMembers   []ClusterMember // Full cluster roster; same on every node
	MinimumBootstrapNodes int      // Minimum reachable nodes before bootstrap
	BootstrapDelay   time.Duration // Wait time before bootstrap when exactly minimum nodes are reachable
	JoinAddr         []string      // Addresses of existing cluster members to join (empty = start new cluster)
	ElectionTimeout  time.Duration // Raft election timeout
	HeartbeatTimeout time.Duration // Raft heartbeat timeout
	SnapshotInterval time.Duration // Snapshot interval
	SnapshotRetain   int           // Number of snapshots to retain
	NTPEnabled       bool          // Whether to enable NTP management
	NTPServers       []string      // External NTP servers
}

// DefaultConfig returns default configuration
func DefaultConfig() *ServerConfig {
	return &ServerConfig{
		ElectionTimeout:  1 * time.Second,
		HeartbeatTimeout: 100 * time.Millisecond,
		SnapshotInterval: 128 * 1024 * 1024,
		SnapshotRetain:   2,
		MinimumBootstrapNodes: 3,
		BootstrapDelay:   0,
		NTPEnabled:       true,
		NTPServers:       []string{"0.pool.ntp.org", "1.pool.ntp.org"},
	}
}

// ClusterMember is a static node descriptor used for symmetric startup.
type ClusterMember struct {
	NodeID   string
	RaftAddr string
}

// TimeModeState represents current time mode state
type TimeModeState struct {
	Mode          TimeMode
	LastUpdated   time.Time
	OperatorID    string      // Who set this mode
	ManualTime    *time.Time  // Only set when Mode is ModeManual
	LastSyncTime  time.Time
	OrderID       uint64      // Order or epoch number for ordering
}

// ServerState represents the state of a server in the cluster
type ServerState struct {
	NodeID        string
	LastMode      TimeMode
	LastUpdated   time.Time
	IsAlive       bool
	LastHeartbeat time.Time
	LastOrderID   uint64      // Last applied order/epoch number
	ManualTime    *time.Time  // Manual time associated with last order (if any)
	ExternalTimeAvailable bool // Whether external time source was reachable on this node
}

// ConsensusLog represents a log entry for the consensus
type ConsensusLog struct {
	Index      uint64
	Term       uint64
	Type       LogType
	Data       []byte
	TimeModes  []TimeModeState // Previous time modes from all servers
	NewMode    TimeMode        // The consensus decided mode
	Timestamp  time.Time
}

// LogType represents the type of consensus log
type LogType int

const (
	LogTypeTimeModeChange LogType = iota
	LogTypeSnapshot
	LogTypeHealthCheck
)

func (lt LogType) String() string {
	switch lt {
	case LogTypeTimeModeChange:
		return "TIMEMODE_CHANGE"
	case LogTypeSnapshot:
		return "SNAPSHOT"
	case LogTypeHealthCheck:
		return "HEALTH_CHECK"
	default:
		return "UNKNOWN"
	}
}
