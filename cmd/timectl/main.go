package main

import (
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"timectl/pkg/config"
	"timectl/pkg/ntp"
	"timectl/pkg/raft"
	"timectl/pkg/server"
)

func main() {
	hostname, _ := os.Hostname()

	// Parse command line flags
	nodeID := flag.String("node-id", hostname, "Unique node ID (defaults to hostname)")
	raftAddr := flag.String("raft-addr", "", "Raft cluster address (auto from --cluster-members when omitted)")
	rpcAddr := flag.String("rpc-addr", "0.0.0.0:5001", "RPC service address")
	httpAddr := flag.String("http-addr", "0.0.0.0:8080", "HTTP API address")
	dataDir := flag.String("data-dir", "./data", "Data directory")
	clusterMembers := flag.String("cluster-members", "", "Comma-separated roster: node1=10.0.0.1:5000,node2=10.0.0.2:5000")
	minBootstrapNodes := flag.Int("min-bootstrap-nodes", 3, "Minimum reachable nodes required before bootstrap")
	joinAddrs := flag.String("join", "", "Comma-separated addresses to join (e.g., 'localhost:5000,localhost:5001')")
	ntpServers := flag.String("ntp-servers", "0.pool.ntp.org,1.pool.ntp.org", "Comma-separated NTP servers")
	enableNTP := flag.Bool("ntp", true, "Enable NTP management")
	verbose := flag.Bool("v", false, "Verbose logging")
	flag.Parse()

	// Create configuration
	cfg := config.DefaultConfig()
	cfg.NodeID = *nodeID
	cfg.RaftAddr = *raftAddr
	cfg.RPCAddr = *rpcAddr
	cfg.HTTPAddr = *httpAddr
	cfg.DataDir = *dataDir
	cfg.NTPEnabled = *enableNTP
	cfg.MinimumBootstrapNodes = *minBootstrapNodes

	if delaySecStr := bootstrapDelayEnv(); delaySecStr != "" {
		delaySec, err := strconv.Atoi(delaySecStr)
		if err != nil || delaySec < 0 {
			fmt.Printf("Error: invalid BOOTSTRAP_DELAY/BOOTSTRA_DELAY value %q (must be non-negative integer seconds)\n", delaySecStr)
			os.Exit(1)
		}
		cfg.BootstrapDelay = time.Duration(delaySec) * time.Second
	}

	if *clusterMembers != "" {
		members, err := parseClusterMembers(*clusterMembers)
		if err != nil {
			fmt.Printf("Error: invalid --cluster-members: %v\n", err)
			os.Exit(1)
		}
		cfg.ClusterMembers = members

		if cfg.RaftAddr == "" {
			selfAddr, ok := raftAddrFromRoster(cfg.NodeID, members)
			if !ok {
				fmt.Printf("Error: node-id %q not found in --cluster-members roster\n", cfg.NodeID)
				os.Exit(1)
			}
			cfg.RaftAddr = selfAddr
		}
	}

	if cfg.RaftAddr == "" {
		fmt.Println("Error: --raft-addr is required when --cluster-members is not set")
		flag.PrintDefaults()
		os.Exit(1)
	}

	if *joinAddrs != "" {
		cfg.JoinAddr = strings.Split(*joinAddrs, ",")
	}

	if *ntpServers != "" {
		cfg.NTPServers = strings.Split(*ntpServers, ",")
	}

	if *verbose {
		fmt.Printf("Configuration:\n")
		fmt.Printf("  Node ID: %s\n", cfg.NodeID)
		fmt.Printf("  Raft Address: %s\n", cfg.RaftAddr)
		fmt.Printf("  RPC Address: %s\n", cfg.RPCAddr)
		fmt.Printf("  HTTP Address: %s\n", cfg.HTTPAddr)
		fmt.Printf("  Data Directory: %s\n", cfg.DataDir)
		fmt.Printf("  Cluster Members: %d\n", len(cfg.ClusterMembers))
		fmt.Printf("  Minimum Bootstrap Nodes: %d\n", cfg.MinimumBootstrapNodes)
		fmt.Printf("  Bootstrap Delay: %s\n", cfg.BootstrapDelay)
		fmt.Printf("  Join Addresses: %v\n", cfg.JoinAddr)
		fmt.Printf("  NTP Servers: %v\n", cfg.NTPServers)
		fmt.Printf("  NTP Enabled: %v\n", cfg.NTPEnabled)
	}

	// Create NTP manager
	ntpManager := ntp.NewManager(cfg.NTPServers)

	if *verbose {
		fmt.Println("[INFO] Creating Raft cluster...")
	}

	// Create Raft cluster
	raftCluster, err := raft.NewCluster(cfg)
	if err != nil {
		fmt.Printf("Error creating Raft cluster: %v\n", err)
		os.Exit(1)
	}
	defer raftCluster.Close()

	if *verbose {
		fmt.Println("[INFO] Raft cluster created successfully")
	}

	// Wait for minimum quorum
	fmt.Println("[INFO] Waiting for cluster quorum (minimum 3 nodes)...")
	maxWait := time.Now().Add(5 * time.Minute)
	for time.Now().Before(maxWait) {
		if raftCluster.IsMinimumQuorum() {
			fmt.Printf("[INFO] Cluster quorum established (%d nodes)\n", raftCluster.GetClusterSize())
			break
		}
		if *verbose {
			fmt.Printf("[DEBUG] Cluster size: %d (need 3)\n", raftCluster.GetClusterSize())
		}
		time.Sleep(1 * time.Second)
	}

	if !raftCluster.IsMinimumQuorum() {
		fmt.Println("[WARN] Cluster does not have minimum quorum yet, continuing anyway...")
	}

	// Create and start server
	if *verbose {
		fmt.Println("[INFO] Starting timectl server...")
	}

	srvr := server.NewServer(cfg, raftCluster, ntpManager)
	if err := srvr.Start(); err != nil {
		fmt.Printf("Error starting server: %v\n", err)
		os.Exit(1)
	}
	defer srvr.Close()

	fmt.Printf("[INFO] timectl server started\n")
	fmt.Printf("[INFO] Raft Address: %s\n", cfg.RaftAddr)
	fmt.Printf("[INFO] RPC Address: %s\n", cfg.RPCAddr)
	fmt.Printf("[INFO] HTTP Address: %s\n", cfg.HTTPAddr)

	// Setup signal handling
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	// Start status monitor
	go statusMonitor(cfg.NodeID, raftCluster, *verbose)

	// Wait for shutdown signal
	sig := <-sigChan
	fmt.Printf("\n[INFO] Received signal: %v\n", sig)
	fmt.Println("[INFO] Shutting down...")

	if err := srvr.Close(); err != nil {
		fmt.Printf("Error closing server: %v\n", err)
	}

	if err := raftCluster.Close(); err != nil {
		fmt.Printf("Error closing Raft cluster: %v\n", err)
	}

	fmt.Println("[INFO] Shutdown complete")
}

// statusMonitor periodically prints cluster status
func statusMonitor(nodeID string, raftCluster *raft.Cluster, verbose bool) {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		state := raftCluster.GetState()
		mode := raftCluster.GetCurrentMode()
		clusterSize := raftCluster.GetClusterSize()
		leader := raftCluster.GetLeader()

		if !verbose {
			continue
		}

		fmt.Printf("[DEBUG] Status - Node: %s, State: %s, Mode: %s, ClusterSize: %d, Leader: %s\n",
			nodeID, state, mode.String(), clusterSize, leader)
	}
}

func parseClusterMembers(raw string) ([]config.ClusterMember, error) {
	parts := strings.Split(raw, ",")
	members := make([]config.ClusterMember, 0, len(parts))
	seen := map[string]struct{}{}

	for _, part := range parts {
		item := strings.TrimSpace(part)
		if item == "" {
			continue
		}
		pair := strings.SplitN(item, "=", 2)
		if len(pair) != 2 {
			return nil, fmt.Errorf("invalid member %q; expected nodeID=host:port", item)
		}
		node := strings.TrimSpace(pair[0])
		addr := strings.TrimSpace(pair[1])
		if node == "" || addr == "" {
			return nil, fmt.Errorf("invalid member %q", item)
		}
		if _, exists := seen[node]; exists {
			return nil, fmt.Errorf("duplicate node ID %q", node)
		}
		seen[node] = struct{}{}
		members = append(members, config.ClusterMember{NodeID: node, RaftAddr: addr})
	}

	if len(members) == 0 {
		return nil, fmt.Errorf("empty cluster roster")
	}

	return members, nil
}

func raftAddrFromRoster(nodeID string, members []config.ClusterMember) (string, bool) {
	for _, member := range members {
		if member.NodeID == nodeID {
			return member.RaftAddr, true
		}
	}
	return "", false
}

func bootstrapDelayEnv() string {
	if v := strings.TrimSpace(os.Getenv("BOOTSTRAP_DELAY")); v != "" {
		return v
	}
	return strings.TrimSpace(os.Getenv("BOOTSTRA_DELAY"))
}
