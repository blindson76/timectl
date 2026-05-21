package raft

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	raftlib "github.com/hashicorp/raft"
	raftboltdb "github.com/hashicorp/raft-boltdb/v2"
	"timectl/pkg/config"
)

// Cluster manages the Raft cluster

type Cluster struct {
	cfg    *config.ServerConfig
	raft   *raftlib.Raft
	fsm    *FSM
	mu     sync.RWMutex
	closed bool

	// Transport for cluster communication
	transport raftlib.Transport

	// Snapshot store
	snapshotStore raftlib.SnapshotStore

	// Log store and stable store
	logStore raftlib.LogStore
}

// FSM returns the FSM instance
func (c *Cluster) FSM() *FSM {
	return c.fsm
}

// NewCluster creates a new Raft cluster
func NewCluster(cfg *config.ServerConfig) (*Cluster, error) {
	if err := os.MkdirAll(cfg.DataDir, 0700); err != nil {
		return nil, fmt.Errorf("failed to create data directory: %w", err)
	}

	c := &Cluster{
		cfg: cfg,
		fsm: NewFSM(),
	}

	// Setup Raft configuration
	raftCfg := raftlib.DefaultConfig()
	raftCfg.ProtocolVersion = raftlib.ProtocolVersionMax
	raftCfg.ElectionTimeout = cfg.ElectionTimeout
	raftCfg.HeartbeatTimeout = cfg.HeartbeatTimeout
	raftCfg.LeaderLeaseTimeout = cfg.HeartbeatTimeout
	raftCfg.TrailingLogs = 10240
	raftCfg.LocalID = raftlib.ServerID(cfg.NodeID)
	raftCfg.Logger = nil // Can be set to custom logger

	// Create snapshot store
	snapshotStore, err := raftlib.NewFileSnapshotStore(cfg.DataDir, cfg.SnapshotRetain, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create snapshot store: %w", err)
	}
	c.snapshotStore = snapshotStore

	// Create log store and stable store using BoltDB
	boltStore, err := raftboltdb.NewBoltStore(filepath.Join(cfg.DataDir, "raft.db"))
	if err != nil {
		return nil, fmt.Errorf("failed to create bolt store: %w", err)
	}
	c.logStore = boltStore

	// Setup transport
	addr, err := net.ResolveTCPAddr("tcp", cfg.RaftAddr)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve raft address: %w", err)
	}

	transport, err := raftlib.NewTCPTransport(cfg.RaftAddr, addr, 3, 10*time.Second, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create transport: %w", err)
	}
	c.transport = transport

	// Create Raft instance
	r, err := raftlib.NewRaft(raftCfg, c.fsm, c.logStore, boltStore, c.snapshotStore, c.transport)
	if err != nil {
		return nil, fmt.Errorf("failed to create raft instance: %w", err)
	}
	c.raft = r

	// Handle bootstrap and cluster joining
	// First, check if this is an existing cluster by looking at the log store
	firstIdx, err := boltStore.FirstIndex()
	if err == nil && firstIdx > 0 {
		// This node has existing state - don't bootstrap
	} else {
		// This is a fresh node. Prefer symmetric startup from static cluster roster.
		if len(cfg.ClusterMembers) > 0 {
			if cfg.MinimumBootstrapNodes < 3 {
				return nil, fmt.Errorf("minimum bootstrap nodes must be >= 3")
			}
			if len(cfg.ClusterMembers) < cfg.MinimumBootstrapNodes {
				return nil, fmt.Errorf("cluster roster has %d members; need at least %d", len(cfg.ClusterMembers), cfg.MinimumBootstrapNodes)
			}

			// Exactly one deterministic node performs bootstrap to avoid split bootstrap.
			if isBootstrapCoordinator(cfg.NodeID, cfg.ClusterMembers) {
				if err := waitForReachableMembers(cfg.ClusterMembers, cfg.MinimumBootstrapNodes, cfg.BootstrapDelay, 2*time.Minute); err != nil {
					return nil, err
				}

				servers := make([]raftlib.Server, 0, len(cfg.ClusterMembers))
				for _, member := range cfg.ClusterMembers {
					servers = append(servers, raftlib.Server{
						ID:      raftlib.ServerID(member.NodeID),
						Address: raftlib.ServerAddress(member.RaftAddr),
					})
				}

				f := r.BootstrapCluster(raftlib.Configuration{Servers: servers})
				if err := f.Error(); err != nil {
					return nil, fmt.Errorf("failed to bootstrap cluster: %w", err)
				}
			}
		} else if len(cfg.JoinAddr) > 0 {
			// Try to join existing cluster
			for _, addr := range cfg.JoinAddr {
				f := r.AddVoter(raftlib.ServerID(cfg.NodeID), raftlib.ServerAddress(addr), 0, 0)
				if err := f.Error(); err == nil {
					break // Successfully added to cluster
				}
			}
		} else {
			// No join address provided - bootstrap as single-node cluster (first node)
			configuration := raftlib.Configuration{
				Servers: []raftlib.Server{
					{
						ID:      raftlib.ServerID(cfg.NodeID),
						Address: raftlib.ServerAddress(cfg.RaftAddr),
					},
				},
			}
			f := r.BootstrapCluster(configuration)
			if err := f.Error(); err != nil {
				return nil, fmt.Errorf("failed to bootstrap cluster: %w", err)
			}
		}
	}

	return c, nil
}

// ApplyCommand applies a command to the Raft log
func (c *Cluster) ApplyCommand(cmd *ApplyCommand) error {
	c.mu.RLock()
	if c.closed {
		c.mu.RUnlock()
		return fmt.Errorf("cluster is closed")
	}
	c.mu.RUnlock()

	data, err := json.Marshal(cmd)
	if err != nil {
		return fmt.Errorf("failed to marshal command: %w", err)
	}

	f := c.raft.Apply(data, 30*time.Second)
	if err := f.Error(); err != nil {
		return fmt.Errorf("failed to apply command: %w", err)
	}

	return nil
}

// SetTimeMode sets the time mode through Raft consensus
func (c *Cluster) SetTimeMode(mode config.TimeMode, operatorID string, manualTime *time.Time, sourceNodeID, sourceNodeRaftAddr string) error {
	return c.SetTimeModeWithOrder(mode, operatorID, manualTime, c.GetCurrentOrderID()+1, sourceNodeID, sourceNodeRaftAddr)
}

// SetTimeModeWithOrder sets the time mode through Raft consensus with an explicit order/epoch.
func (c *Cluster) SetTimeModeWithOrder(mode config.TimeMode, operatorID string, manualTime *time.Time, orderID uint64, sourceNodeID, sourceNodeRaftAddr string) error {
	state := config.TimeModeState{
		Mode:               mode,
		OperatorID:         operatorID,
		ManualTime:         manualTime,
		OrderID:            orderID,
		OrderCreatedAt:     time.Now(),
		SourceNodeID:       sourceNodeID,
		SourceNodeRaftAddr: sourceNodeRaftAddr,
	}

	stateJSON, err := json.Marshal(state)
	if err != nil {
		return fmt.Errorf("failed to marshal state: %w", err)
	}

	cmd := &ApplyCommand{
		Type: CommandTypeSetTimeMode,
		Data: string(stateJSON),
	}

	return c.ApplyCommand(cmd)
}

// GetCurrentOrderID returns the currently applied order/epoch number.
func (c *Cluster) GetCurrentOrderID() uint64 {
	return c.fsm.GetLastOrderID()
}

// SyncServerStates synchronizes server states through Raft
func (c *Cluster) SyncServerStates(states []config.ServerState) error {
	statesJSON, err := json.Marshal(states)
	if err != nil {
		return fmt.Errorf("failed to marshal states: %w", err)
	}

	cmd := &ApplyCommand{
		Type: CommandTypeSyncServerState,
		Data: string(statesJSON),
	}

	return c.ApplyCommand(cmd)
}

// GetCurrentMode returns the current time mode
func (c *Cluster) GetCurrentMode() config.TimeMode {
	return c.fsm.GetCurrentMode()
}

// GetServerStates returns all known server states
func (c *Cluster) GetServerStates() map[string]config.ServerState {
	return c.fsm.GetServerStates()
}

// GetTimeModeState returns the current time mode state
func (c *Cluster) GetTimeModeState() config.TimeModeState {
	return c.fsm.GetTimeModeState()
}

// IsLeader returns whether this node is the leader
func (c *Cluster) IsLeader() bool {
	return c.raft.State() == raftlib.Leader
}

// GetLeader returns the current leader
func (c *Cluster) GetLeader() string {
	return string(c.raft.Leader())
}

// GetState returns the current Raft state
func (c *Cluster) GetState() string {
	state := c.raft.State()
	switch state {
	case raftlib.Leader:
		return "LEADER"
	case raftlib.Candidate:
		return "CANDIDATE"
	case raftlib.Follower:
		return "FOLLOWER"
	case raftlib.Shutdown:
		return "SHUTDOWN"
	default:
		return "UNKNOWN"
	}
}

// GetStats returns Raft statistics
func (c *Cluster) GetStats() map[string]string {
	return c.raft.Stats()
}

// Close closes the cluster
func (c *Cluster) Close() error {
	c.mu.Lock()
	c.closed = true
	c.mu.Unlock()

	if c.raft != nil {
		f := c.raft.Shutdown()
		if err := f.Error(); err != nil {
			return fmt.Errorf("failed to shutdown raft: %w", err)
		}
	}

	return nil
}

// WaitForLeader waits for a leader to be elected
func (c *Cluster) WaitForLeader(timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			if c.raft.State() != raftlib.Shutdown && c.raft.Leader() != "" {
				return nil
			}
			if time.Now().After(deadline) {
				return fmt.Errorf("timeout waiting for leader")
			}
		}
	}
}

// GetClusterSize returns the number of nodes in the cluster
func (c *Cluster) GetClusterSize() int {
	config := c.raft.GetConfiguration()
	if err := config.Error(); err != nil {
		return 0
	}
	return len(config.Configuration().Servers)
}

// IsMinimumQuorum checks if we have minimum 3 nodes for quorum
func (c *Cluster) IsMinimumQuorum() bool {
	return c.GetClusterSize() >= 3
}

// AddPeer adds a peer to the cluster
func (c *Cluster) AddPeer(nodeID string, raftAddr string) error {
	f := c.raft.AddVoter(raftlib.ServerID(nodeID), raftlib.ServerAddress(raftAddr), 0, 0)
	return f.Error()
}

// RemovePeer removes a peer from the cluster
func (c *Cluster) RemovePeer(nodeID string) error {
	f := c.raft.RemoveServer(raftlib.ServerID(nodeID), 0, 0)
	return f.Error()
}

// BootstrapAsLeader bootstraps this node as cluster leader (symmetric operation)
// Can be called by any node if no leader exists yet
func (c *Cluster) BootstrapAsLeader() error {
	// Check if already part of a cluster
	cfg := c.raft.GetConfiguration()
	if err := cfg.Error(); err != nil {
		return fmt.Errorf("failed to get configuration: %w", err)
	}

	if len(cfg.Configuration().Servers) > 1 {
		return fmt.Errorf("cluster already has members")
	}

	// Bootstrap as single-node cluster
	configuration := raftlib.Configuration{
		Servers: []raftlib.Server{
			{
				ID:      raftlib.ServerID(c.cfg.NodeID),
				Address: raftlib.ServerAddress(c.cfg.RaftAddr),
			},
		},
	}

	f := c.raft.BootstrapCluster(configuration)
	return f.Error()
}

func isBootstrapCoordinator(nodeID string, members []config.ClusterMember) bool {
	ids := make([]string, 0, len(members))
	for _, member := range members {
		ids = append(ids, member.NodeID)
	}
	sort.Strings(ids)
	return len(ids) > 0 && ids[0] == nodeID
}

func waitForReachableMembers(members []config.ClusterMember, minReachable int, bootstrapDelay, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()
	var minReachedAt time.Time

	for {
		reachable := 0
		for _, member := range members {
			conn, err := net.DialTimeout("tcp", member.RaftAddr, 400*time.Millisecond)
			if err == nil {
				reachable++
				_ = conn.Close()
			}
		}

		if reachable > minReachable {
			return nil
		}

		if reachable == minReachable {
			if bootstrapDelay <= 0 {
				return nil
			}
			if minReachedAt.IsZero() {
				minReachedAt = time.Now()
			} else if time.Since(minReachedAt) >= bootstrapDelay {
				return nil
			}
		} else {
			minReachedAt = time.Time{}
		}

		if time.Now().After(deadline) {
			return fmt.Errorf("bootstrap blocked: reachable nodes %d, required %d", reachable, minReachable)
		}

		<-ticker.C
	}
}
