package server

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"net/http"
	"sync"
	"time"

	"timectl/pkg/config"
	"timectl/pkg/ntp"
	"timectl/pkg/raft"
)

// Server implements the timectl service
type Server struct {
	cfg          *config.ServerConfig
	raftCluster  *raft.Cluster
	ntpManager   *ntp.Manager
	httpServer   *http.Server
	mu           sync.RWMutex
	closed       bool
	heartbeatTTL time.Duration
	lastExternalTimeAvailable bool
	hasAppliedDecision bool
	lastAppliedOrderID uint64
	lastAppliedMode config.TimeMode
	lastAppliedManualTime *time.Time
}

type persistedStatus struct {
	Mode                  string     `json:"mode"`
	ManualTime            *time.Time `json:"manual_time,omitempty"`
	OrderID               uint64     `json:"order_id"`
	LastUpdated           time.Time  `json:"last_updated"`
	ExternalTimeAvailable bool       `json:"external_time_available"`
}

// NewServer creates a new timectl server
func NewServer(cfg *config.ServerConfig, raftCluster *raft.Cluster, ntpManager *ntp.Manager) *Server {
	return &Server{
		cfg:          cfg,
		raftCluster:  raftCluster,
		ntpManager:   ntpManager,
		heartbeatTTL: 5 * time.Second,
	}
}

// Start starts the server
func (s *Server) Start() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return fmt.Errorf("server is already closed")
	}
	s.mu.Unlock()

	// Start HTTP server
	if err := s.startHTTP(); err != nil {
		return fmt.Errorf("failed to start HTTP server: %w", err)
	}

	// Wait for leader election with extended timeout for larger clusters
	if err := s.raftCluster.WaitForLeader(60 * time.Second); err != nil {
		fmt.Printf("[WARN] Timeout waiting for leader (cluster may not have quorum yet)\n")
	}

	if err := s.reportStartupStatus(); err != nil {
		fmt.Printf("[WARN] Failed to report startup status: %v\n", err)
	}

	if err := s.startupClusterDecision(); err != nil {
		fmt.Printf("[WARN] Startup cluster decision failed: %v\n", err)
	}

	// Start background tasks
	go s.heartbeatLoop()
	go s.reconciliationLoop()

	return nil
}

// startHTTP starts the HTTP server
func (s *Server) startHTTP() error {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/status", s.handleStatus)
	mux.HandleFunc("/api/timemode", s.handleTimeMode)
	mux.HandleFunc("/api/cluster", s.handleCluster)
	mux.HandleFunc("/api/cluster/bootstrap", s.handleBootstrap)
	mux.HandleFunc("/api/cluster/peers", s.handlePeers)
	mux.HandleFunc("/api/servers", s.handleServers)
	mux.HandleFunc("/api/logs", s.handleLogs)
	mux.HandleFunc("/health", s.handleHealth)

	s.httpServer = &http.Server{
		Addr:    s.cfg.HTTPAddr,
		Handler: mux,
	}

	go func() {
		if err := s.httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			fmt.Printf("HTTP server error: %v\n", err)
		}
	}()

	return nil
}

// heartbeatLoop sends periodic heartbeats to synchronize state
func (s *Server) heartbeatLoop() {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		s.mu.RLock()
		if s.closed {
			s.mu.RUnlock()
			return
		}
		s.mu.RUnlock()

		// Collect current server state
		current := s.raftCluster.GetTimeModeState()
		state := config.ServerState{
			NodeID:                s.cfg.NodeID,
			LastMode:              current.Mode,
			LastUpdated:           time.Now(),
			IsAlive:               true,
			LastHeartbeat:         time.Now(),
			LastOrderID:           current.OrderID,
			ManualTime:            current.ManualTime,
			ExternalTimeAvailable: s.lastExternalTimeAvailable,
		}

		// Apply heartbeat to Raft
		stateJSON, _ := json.Marshal([]config.ServerState{state})
		cmd := &raft.ApplyCommand{
			Type: raft.CommandTypeSyncServerState,
			Data: string(stateJSON),
		}
		_ = s.raftCluster.ApplyCommand(cmd)
	}
}

// reconciliationLoop periodically reconciles system state
func (s *Server) reconciliationLoop() {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		s.mu.RLock()
		if s.closed {
			s.mu.RUnlock()
			return
		}
		s.mu.RUnlock()

		state := s.raftCluster.GetTimeModeState()
		if !s.shouldApplyDecision(state) {
			continue
		}

		// Apply mode based on consensus
		if err := s.applyTimeMode(state.Mode); err != nil {
			fmt.Printf("Error applying time mode: %v\n", err)
			continue
		}

		s.markDecisionApplied(state)

		if err := s.persistCurrentStatus(); err != nil {
			fmt.Printf("Error persisting current status: %v\n", err)
		}
	}
}

func (s *Server) shouldApplyDecision(state config.TimeModeState) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if !s.hasAppliedDecision {
		return true
	}

	if state.OrderID != s.lastAppliedOrderID {
		return true
	}

	if state.Mode != s.lastAppliedMode {
		return true
	}

	if !sameTimePtr(state.ManualTime, s.lastAppliedManualTime) {
		return true
	}

	return false
}

func (s *Server) markDecisionApplied(state config.TimeModeState) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.hasAppliedDecision = true
	s.lastAppliedOrderID = state.OrderID
	s.lastAppliedMode = state.Mode
	if state.ManualTime != nil {
		t := *state.ManualTime
		s.lastAppliedManualTime = &t
	} else {
		s.lastAppliedManualTime = nil
	}
}

func sameTimePtr(a, b *time.Time) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return a.Equal(*b)
}

// applyTimeMode applies the consensus time mode to the system
func (s *Server) applyTimeMode(mode config.TimeMode) error {
	if err := s.ntpManager.Start(); err != nil {
		return fmt.Errorf("failed to start ntpd service: %w", err)
	}

	switch mode {
	case config.ModeAuto:
		externalAvailable := s.detectExternalTimeAvailable()
		s.lastExternalTimeAvailable = externalAvailable

		if !externalAvailable {
			fmt.Printf("[WARN] External time source unavailable; cluster should switch to MANUAL if needed\n")
			return nil
		}

		if s.raftCluster.IsLeader() {
			if err := s.ntpManager.SetNTPServers(s.cfg.NTPServers); err != nil {
				return err
			}
		} else {
			// Orphan mode uses local clock as source for non-leader nodes.
			if err := s.ntpManager.SetNTPServers([]string{"127.127.1.0"}); err != nil {
				return err
			}
		}
		return s.ntpManager.EnableNTPSync()

	case config.ModeManual:
		state := s.raftCluster.GetTimeModeState()
		if state.ManualTime != nil {
			if err := s.ntpManager.SetSystemTime(*state.ManualTime); err != nil {
				return err
			}
		}
		if err := s.ntpManager.SetNTPServers([]string{"127.127.1.0"}); err != nil {
			return err
		}
		return s.ntpManager.EnableNTPSync()

	default:
		return fmt.Errorf("unknown time mode: %v", mode)
	}
}

// handleStatus handles GET /api/status
func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	state := s.raftCluster.GetTimeModeState()
	stats := s.raftCluster.GetStats()

	response := map[string]interface{}{
		"node_id":         s.cfg.NodeID,
		"state":           s.raftCluster.GetState(),
		"leader":          s.raftCluster.GetLeader(),
		"is_leader":       s.raftCluster.IsLeader(),
		"current_mode":    state.Mode.String(),
		"order_id":        state.OrderID,
		"last_updated":    state.LastUpdated,
		"manual_time":     state.ManualTime,
		"external_time_available": s.lastExternalTimeAvailable,
		"cluster_size":    s.raftCluster.GetClusterSize(),
		"has_quorum":      s.raftCluster.IsMinimumQuorum(),
		"raft_stats":      stats,
	}

	json.NewEncoder(w).Encode(response)
}

// handleTimeMode handles GET/POST /api/timemode
func (s *Server) handleTimeMode(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if r.Method == "GET" {
		state := s.raftCluster.GetTimeModeState()
		response := map[string]interface{}{
			"mode":         state.Mode.String(),
			"order_id":     state.OrderID,
			"last_updated": state.LastUpdated,
			"operator_id":  state.OperatorID,
		}
		if state.ManualTime != nil {
			response["manual_time"] = state.ManualTime
		}
		json.NewEncoder(w).Encode(response)
	} else if r.Method == "POST" {
		if !s.raftCluster.IsLeader() {
			w.WriteHeader(http.StatusForbidden)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"success": false,
				"message": "not the leader",
			})
			return
		}

		var req map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"success": false,
				"message": fmt.Sprintf("invalid request: %v", err),
			})
			return
		}

		modeStr, ok := req["mode"].(string)
		if !ok {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"success": false,
				"message": "missing mode",
			})
			return
		}

		var mode config.TimeMode
		if modeStr == "AUTO" {
			mode = config.ModeAuto
		} else if modeStr == "MANUAL" {
			mode = config.ModeManual
		} else {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"success": false,
				"message": "invalid mode",
			})
			return
		}

		var manualTime *time.Time
		if manualTimeStr, ok := req["manual_time"].(string); ok {
			t, err := time.Parse(time.RFC3339, manualTimeStr)
			if err != nil {
				w.WriteHeader(http.StatusBadRequest)
				json.NewEncoder(w).Encode(map[string]interface{}{
					"success": false,
					"message": fmt.Sprintf("invalid time format: %v", err),
				})
				return
			}
			manualTime = &t
		}

		operatorID, _ := req["operator_id"].(string)

		if err := s.raftCluster.SetTimeMode(mode, operatorID, manualTime); err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"success": false,
				"message": fmt.Sprintf("failed to set time mode: %v", err),
			})
			return
		}

		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"message": "time mode updated",
		})
	}
}

// handleCluster handles GET /api/cluster
func (s *Server) handleCluster(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	response := map[string]interface{}{
		"node_id":     s.cfg.NodeID,
		"state":       s.raftCluster.GetState(),
		"leader":      s.raftCluster.GetLeader(),
		"cluster_size": s.raftCluster.GetClusterSize(),
		"has_quorum":  s.raftCluster.IsMinimumQuorum(),
		"raft_stats":  s.raftCluster.GetStats(),
	}

	json.NewEncoder(w).Encode(response)
}

// handleServers handles GET /api/servers
func (s *Server) handleServers(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	serverStates := s.raftCluster.GetServerStates()
	servers := make([]map[string]interface{}, 0)

	for _, state := range serverStates {
		servers = append(servers, map[string]interface{}{
			"node_id":         state.NodeID,
			"last_mode":       state.LastMode.String(),
			"last_order_id":   state.LastOrderID,
			"manual_time":     state.ManualTime,
			"external_time_available": state.ExternalTimeAvailable,
			"last_updated":    state.LastUpdated,
			"is_alive":        state.IsAlive,
			"last_heartbeat":  state.LastHeartbeat,
		})
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"servers": servers,
	})
}

func (s *Server) startupClusterDecision() error {
	if !s.raftCluster.IsLeader() {
		return nil
	}

	// Give other members a brief window to report startup status.
	time.Sleep(3 * time.Second)

	states := s.raftCluster.GetServerStates()
	if len(states) == 0 {
		return nil
	}

	var chosen *config.ServerState
	for _, st := range states {
		candidate := st
		if chosen == nil || candidate.LastOrderID > chosen.LastOrderID || (candidate.LastOrderID == chosen.LastOrderID && candidate.LastUpdated.After(chosen.LastUpdated)) {
			chosen = &candidate
		}
	}

	if chosen == nil {
		return nil
	}

	operatorID := "startup-sync"
	if err := s.raftCluster.SetTimeModeWithOrder(chosen.LastMode, operatorID, chosen.ManualTime, chosen.LastOrderID); err != nil {
		return err
	}

	return nil
}

func (s *Server) reportStartupStatus() error {
	persisted, err := s.loadPersistedStatus()
	if err != nil {
		return err
	}

	mode := config.ModeAuto
	if persisted.Mode == config.ModeManual.String() {
		mode = config.ModeManual
	}

	state := config.ServerState{
		NodeID:                s.cfg.NodeID,
		LastMode:              mode,
		LastUpdated:           persisted.LastUpdated,
		IsAlive:               true,
		LastHeartbeat:         time.Now(),
		LastOrderID:           persisted.OrderID,
		ManualTime:            persisted.ManualTime,
		ExternalTimeAvailable: persisted.ExternalTimeAvailable,
	}

	return s.raftCluster.SyncServerStates([]config.ServerState{state})
}

func (s *Server) loadPersistedStatus() (*persistedStatus, error) {
	path := filepath.Join(s.cfg.DataDir, "last-status.json")
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &persistedStatus{
				Mode:        config.ModeAuto.String(),
				OrderID:     s.raftCluster.GetCurrentOrderID(),
				LastUpdated: time.Now(),
			}, nil
		}
		return nil, err
	}

	var ps persistedStatus
	if err := json.Unmarshal(b, &ps); err != nil {
		return nil, err
	}
	if ps.LastUpdated.IsZero() {
		ps.LastUpdated = time.Now()
	}
	if ps.Mode == "" {
		ps.Mode = config.ModeAuto.String()
	}
	return &ps, nil
}

func (s *Server) persistCurrentStatus() error {
	state := s.raftCluster.GetTimeModeState()
	ps := persistedStatus{
		Mode:                  state.Mode.String(),
		ManualTime:            state.ManualTime,
		OrderID:               state.OrderID,
		LastUpdated:           time.Now(),
		ExternalTimeAvailable: s.lastExternalTimeAvailable,
	}

	b, err := json.MarshalIndent(ps, "", "  ")
	if err != nil {
		return err
	}

	if err := os.MkdirAll(s.cfg.DataDir, 0700); err != nil {
		return err
	}
	path := filepath.Join(s.cfg.DataDir, "last-status.json")
	return os.WriteFile(path, b, 0600)
}

func (s *Server) detectExternalTimeAvailable() bool {
	if !s.cfg.NTPEnabled {
		return false
	}
	if _, err := s.ntpManager.QueryNTPOffset(); err != nil {
		return false
	}
	return true
}

// handleLogs handles GET /api/logs
func (s *Server) handleLogs(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	
	response := map[string]interface{}{
		"message": "log endpoint not yet implemented",
	}

	json.NewEncoder(w).Encode(response)
}

// handleBootstrap handles POST /api/cluster/bootstrap
// Allows any node to bootstrap as leader if cluster not yet initialized
func (s *Server) handleBootstrap(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if r.Method != "POST" {
		w.WriteHeader(http.StatusMethodNotAllowed)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error": "method not allowed",
		})
		return
	}

	// Try to bootstrap this node as leader
	if err := s.raftCluster.BootstrapAsLeader(); err != nil {
		w.WriteHeader(http.StatusConflict)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"message": fmt.Sprintf("failed to bootstrap: %v", err),
		})
		return
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": "node bootstrapped as cluster leader",
		"node_id": s.cfg.NodeID,
	})
}

// handlePeers handles GET/POST /api/cluster/peers
// GET: list all peers, POST: add a peer
func (s *Server) handlePeers(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if r.Method == "GET" {
		// Get all peer information
		stats := s.raftCluster.GetStats()
		response := map[string]interface{}{
			"node_id": s.cfg.NodeID,
			"state":   s.raftCluster.GetState(),
			"leader":  s.raftCluster.GetLeader(),
			"peers":   stats["peers"],
		}
		json.NewEncoder(w).Encode(response)
		return
	}

	if r.Method == "POST" {
		if !s.raftCluster.IsLeader() {
			w.WriteHeader(http.StatusForbidden)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"success": false,
				"message": "only leader can add peers",
			})
			return
		}

		var req map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"success": false,
				"message": fmt.Sprintf("invalid request: %v", err),
			})
			return
		}

		nodeID, ok := req["node_id"].(string)
		if !ok {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"success": false,
				"message": "missing node_id",
			})
			return
		}

		raftAddr, ok := req["raft_addr"].(string)
		if !ok {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"success": false,
				"message": "missing raft_addr",
			})
			return
		}

		if err := s.raftCluster.AddPeer(nodeID, raftAddr); err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"success": false,
				"message": fmt.Sprintf("failed to add peer: %v", err),
			})
			return
		}

		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"message": fmt.Sprintf("peer %s added", nodeID),
		})
		return
	}

	w.WriteHeader(http.StatusMethodNotAllowed)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"error": "method not allowed",
	})
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	state := s.raftCluster.GetState()
	status := "healthy"
	if state == "SHUTDOWN" {
		status = "unhealthy"
	}

	response := map[string]interface{}{
		"status": status,
		"state":  state,
	}

	json.NewEncoder(w).Encode(response)
}

// Close closes the server
func (s *Server) Close() error {
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()

	if s.httpServer != nil {
		s.httpServer.Close()
	}

	return nil
}
