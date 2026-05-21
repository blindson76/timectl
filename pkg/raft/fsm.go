
package raft


import (
	"encoding/json"
	"io"
	"sync"
	"time"

	raftlib "github.com/hashicorp/raft"
	"timectl/pkg/config"
)

// GetLastOrderID returns the last applied order/epoch number.
func (f *FSM) GetLastOrderID() uint64 {
	f.mu.RLock()
	defer f.mu.RUnlock()
	if f.currentOrderID > 0 {
		return f.currentOrderID
	}
	return f.lastLogIndex
}

// FSM implements raft.FSM interface for managing time mode state
type FSM struct {
	mu                sync.RWMutex
	currentMode       config.TimeMode
	currentOrderID    uint64
	lastUpdated       time.Time
	operatorID        string
	manualTime        *time.Time
	lastSyncTime      time.Time
	serverStates      map[string]config.ServerState // state of all servers
	consensusLog      []config.ConsensusLog
	lastLogIndex      uint64
}

// NewFSM creates a new FSM
func NewFSM() *FSM {
	return &FSM{
		currentMode:  config.ModeAuto, // Default to AUTO mode
		currentOrderID: 0,
		lastUpdated:  time.Now(),
		serverStates: make(map[string]config.ServerState),
		consensusLog: make([]config.ConsensusLog, 0),
		lastLogIndex: 0,
	}
}

// Apply applies a log entry to the FSM
func (f *FSM) Apply(log *raftlib.Log) interface{} {
	f.mu.Lock()
	defer f.mu.Unlock()

	cmd := &ApplyCommand{}
	if err := json.Unmarshal(log.Data, cmd); err != nil {
		return err
	}

	f.lastLogIndex = log.Index

	switch cmd.Type {
	case CommandTypeSetTimeMode:
		return f.applySetTimeMode(cmd, log)
	case CommandTypeSyncServerState:
		return f.applySyncServerState(cmd, log)
	case CommandTypeHealthCheck:
		return f.applyHealthCheck(cmd, log)
	default:
		return "unknown command type"
	}
}

// applySetTimeMode applies a time mode change command
func (f *FSM) applySetTimeMode(cmd *ApplyCommand, log *raftlib.Log) interface{} {
	state := &config.TimeModeState{}
	if err := json.Unmarshal([]byte(cmd.Data), state); err != nil {
		return err
	}

	// Perform consensus decision
	decidedMode := f.makeConsensusDecision(state)
	
	f.currentMode = decidedMode
	if state.OrderID > 0 {
		f.currentOrderID = state.OrderID
	} else {
		f.currentOrderID = log.Index
	}
	f.lastUpdated = time.Now()
	f.operatorID = state.OperatorID
	if state.ManualTime != nil {
		f.manualTime = state.ManualTime
	}

	// Log the consensus decision
	consensusEntry := config.ConsensusLog{
		Index:      log.Index,
		Term:       log.Term,
		Type:       config.LogTypeTimeModeChange,
		Timestamp:  time.Now(),
		NewMode:    decidedMode,
	}
	f.consensusLog = append(f.consensusLog, consensusEntry)

	return map[string]interface{}{
		"success": true,
		"mode":    decidedMode.String(),
	}
}

// applySyncServerState applies server state synchronization
func (f *FSM) applySyncServerState(cmd *ApplyCommand, log *raftlib.Log) interface{} {
	states := make([]config.ServerState, 0)
	if err := json.Unmarshal([]byte(cmd.Data), &states); err != nil {
		return err
	}

	for _, state := range states {
		f.serverStates[state.NodeID] = state
	}
	f.lastSyncTime = time.Now()

	return map[string]interface{}{
		"success": true,
		"synced":  len(states),
	}
}

// applyHealthCheck applies a health check
func (f *FSM) applyHealthCheck(cmd *ApplyCommand, log *raftlib.Log) interface{} {
	nodeID := cmd.Data
	if state, exists := f.serverStates[nodeID]; exists {
		state.LastHeartbeat = time.Now()
		state.IsAlive = true
		f.serverStates[nodeID] = state
	}
	return "health check applied"
}

// makeConsensusDecision chooses the most recent order/epoch from all nodes and uses its mode
func (f *FSM) makeConsensusDecision(newState *config.TimeModeState) config.TimeMode {
   // Collect all reported orders (including newState)
   type orderInfo struct {
	   Mode    config.TimeMode
	   OrderID uint64
   }
   orders := []orderInfo{
	   {Mode: newState.Mode, OrderID: newState.OrderID},
   }
   for _, state := range f.serverStates {
	   orders = append(orders, orderInfo{Mode: state.LastMode, OrderID: state.LastOrderID})
   }
   // Find the order with the highest OrderID
   var latest orderInfo
   for i, o := range orders {
	   if i == 0 || o.OrderID > latest.OrderID {
		   latest = o
	   }
   }
   return latest.Mode
}

// Snapshot creates a snapshot of current FSM state
func (f *FSM) Snapshot() (raftlib.FSMSnapshot, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()

	return &FSMSnapshot{
		currentMode:  f.currentMode,
		lastUpdated:  f.lastUpdated,
		operatorID:   f.operatorID,
		manualTime:   f.manualTime,
		lastSyncTime: f.lastSyncTime,
		serverStates: f.serverStates,
		lastLogIndex: f.lastLogIndex,
	}, nil
}

// Restore restores FSM state from a snapshot
func (f *FSM) Restore(snapshot io.ReadCloser) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	defer snapshot.Close()

	fsnap := &FSMSnapshot{}
	decoder := json.NewDecoder(snapshot)
	if err := decoder.Decode(fsnap); err != nil {
		return err
	}

	f.currentMode = fsnap.currentMode
	f.lastUpdated = fsnap.lastUpdated
	f.operatorID = fsnap.operatorID
	f.manualTime = fsnap.manualTime
	f.lastSyncTime = fsnap.lastSyncTime
	f.serverStates = fsnap.serverStates
	f.lastLogIndex = fsnap.lastLogIndex

	return nil
}

// GetCurrentMode returns the current time mode
func (f *FSM) GetCurrentMode() config.TimeMode {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.currentMode
}

// GetServerStates returns all known server states
func (f *FSM) GetServerStates() map[string]config.ServerState {
	f.mu.RLock()
	defer f.mu.RUnlock()

	states := make(map[string]config.ServerState)
	for k, v := range f.serverStates {
		states[k] = v
	}
	return states
}

// GetTimeModeState returns the current time mode state
func (f *FSM) GetTimeModeState() config.TimeModeState {
	f.mu.RLock()
	defer f.mu.RUnlock()

	return config.TimeModeState{
		Mode:         f.currentMode,
		OrderID:      f.currentOrderID,
		LastUpdated:  f.lastUpdated,
		OperatorID:   f.operatorID,
		ManualTime:   f.manualTime,
		LastSyncTime: f.lastSyncTime,
	}
}

// FSMSnapshot implements raft.FSMSnapshot
type FSMSnapshot struct {
	currentMode  config.TimeMode
	lastUpdated  time.Time
	operatorID   string
	manualTime   *time.Time
	lastSyncTime time.Time
	serverStates map[string]config.ServerState
	lastLogIndex uint64
}

// Persist persists the snapshot
func (fs *FSMSnapshot) Persist(sink raftlib.SnapshotSink) error {
	defer sink.Close()
	
	b, err := json.Marshal(fs)
	if err != nil {
		sink.Cancel()
		return err
	}

	if _, err := sink.Write(b); err != nil {
		sink.Cancel()
		return err
	}

	return sink.Close()
}

// Release releases the snapshot
func (fs *FSMSnapshot) Release() {
	// No resources to release
}

// ApplyCommand represents a command to apply via Raft
type ApplyCommand struct {
	Type CommandType `json:"type"`
	Data string      `json:"data"`
}

// CommandType represents the type of command
type CommandType int

const (
	CommandTypeSetTimeMode CommandType = iota
	CommandTypeSyncServerState
	CommandTypeHealthCheck
)
