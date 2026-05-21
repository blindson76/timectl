# timectl - Distributed Time Control System

A distributed, consensus-based time control system for managing NTP synchronization across a cluster of Windows/Linux servers. Uses HashiCorp Raft for distributed consensus.

## Features

- **Distributed Consensus**: Uses Raft algorithm to reach consensus on time mode across the cluster
- **Two Time Modes**:
  - **AUTO**: System synchronizes time from external NTP sources automatically
  - **MANUAL**: Operator provides manual time input via API
- **Minimum Quorum**: Cluster requires at least 3 nodes to operate and reach consensus
- **NTP Management**: Runs a user-specified command (not a service) on every new cluster time mode decision
- **gRPC Interface**: Type-safe RPC interface for inter-node communication
- **HTTP API**: REST API for status, configuration, and management
- **Leader Election**: Automatic leader election and failover handling
- **Persistent State**: State persistence using BoltDB

## Architecture

### Components

1. **Raft Cluster**: Consensus engine for distributed decision-making
2. **FSM (Finite State Machine)**: State machine that maintains time mode consensus
3. **NTP Manager**: Handles NTP service management and time synchronization
4. **gRPC Server**: Provides distributed RPC interface
5. **HTTP Server**: Provides REST API for management

### Key Design Decisions

- **Consensus Decision**: Time mode is decided by majority vote across all nodes
- **Heartbeat Mechanism**: Each node sends periodic heartbeats with its current state
- **State Reconciliation**: Periodic reconciliation ensures all nodes apply the consensus mode
- **Persistent Storage**: Uses BoltDB for reliable log and state storage

## Building

```bash
cd /workspaces/timectl
go mod tidy
go build -o bin/timectl ./cmd/timectl
```

## Usage

### All Nodes Use the Same Flags and Settings

Every node starts with the same command-line flags and the same cluster roster. Node identity defaults to hostname (`--node-id` is optional), and each node automatically picks its Raft address from the shared `--cluster-members` value.

Bootstrap is deterministic and symmetric:

- The lexicographically smallest node ID in `--cluster-members` coordinates bootstrap.
- Bootstrap is blocked until at least `--min-bootstrap-nodes` (default `3`) are reachable.
- Other nodes start with the same flags and join automatically after bootstrap.

### Common Start Command for All 6 Nodes

Run the same command on every server:

```bash
./bin/timectl \
  --cluster-members "node1=10.0.0.1:5000,node2=10.0.0.2:5000,node3=10.0.0.3:5000,node4=10.0.0.4:5000,node5=10.0.0.5:5000,node6=10.0.0.6:5000" \
  --rpc-addr 0.0.0.0:5001 \
  --http-addr 0.0.0.0:8080 \
  --data-dir ./data \
  --min-bootstrap-nodes 3 \
  --ntp-servers "0.pool.ntp.org,1.pool.ntp.org" \
  -v
```

Requirement for zero per-node flags: hostnames must match node IDs in `--cluster-members` (`node1` ... `node6`).


### NTP Apply Command

You can specify a custom command to run every time a new cluster time mode decision is applied. This replaces all built-in NTP/ntpd service logic. The command is invoked with environment variables:

- `TIMECTL_MODE` ("AUTO" or "MANUAL")
- `TIMECTL_ORDER_ID` (the consensus order/epoch number)
- `TIMECTL_MANUAL_TIME` (RFC3339, only if mode is MANUAL)

Specify the command via:
- `--ntp-apply-cmd '/path/to/script --flag'` (CLI flag)
- or environment variable `TIMECTL_NTP_APPLY_CMD`

If not set, no command is run.

### Configuration Options

- `--cluster-members`: Shared roster for all nodes in format `nodeID=host:port,...`
- `--node-id`: Node identifier (optional, defaults to hostname)
- `--raft-addr`: Raft address (optional when `--cluster-members` is used)
- `--rpc-addr`: RPC service address (default: `0.0.0.0:5001`)
- `--http-addr`: HTTP API address (default: `0.0.0.0:8080`)
- `--data-dir`: Data storage directory (default: ./data)
- `--min-bootstrap-nodes`: Minimum reachable nodes required before bootstrap (default: `3`)
- `--ntp-servers`: Comma-separated NTP servers (default: 0.pool.ntp.org,1.pool.ntp.org)
- `--ntp`: Enable NTP management (default: true)
- `--ntp-apply-cmd`: Command to run when a new cluster time mode is applied (overrides all service logic)
- `-v`: Enable verbose logging

## HTTP API

### Get Current Status

```bash
curl http://localhost:8080/api/status
```

Response:
```json
{
  "node_id": "node1",
  "state": "LEADER",
  "leader": "node1",
  "is_leader": true,
  "current_mode": "AUTO",
  "last_updated": "2024-01-15T10:30:00Z",
  "cluster_size": 3,
  "has_quorum": true,
  "raft_stats": {...}
}
```

### Get Current Time Mode

```bash
curl http://localhost:8080/api/timemode
```

Response:
```json
{
  "mode": "AUTO",
  "last_updated": "2024-01-15T10:30:00Z",
  "operator_id": "admin"
}
```

### Set Time Mode (Leader only)

Switch to MANUAL mode:
```bash
curl -X POST http://localhost:8080/api/timemode \
  -H "Content-Type: application/json" \
  -d '{
    "mode": "MANUAL",
    "operator_id": "admin",
    "manual_time": "2024-01-15T10:30:00Z"
  }'
```

Switch back to AUTO mode:
```bash
curl -X POST http://localhost:8080/api/timemode \
  -H "Content-Type: application/json" \
  -d '{
    "mode": "AUTO",
    "operator_id": "admin"
  }'
```

### Get Cluster Status

```bash
curl http://localhost:8080/api/cluster
```

Response:
```json
{
  "node_id": "node1",
  "state": "LEADER",
  "leader": "node1",
  "cluster_size": 3,
  "has_quorum": true,
  "raft_stats": {...}
}
```

### Get Server States

```bash
curl http://localhost:8080/api/servers
```

Response:
```json
{
  "servers": [
    {
      "node_id": "node1",
      "last_mode": "AUTO",
      "last_updated": "2024-01-15T10:35:00Z",
      "is_alive": true,
      "last_heartbeat": "2024-01-15T10:35:00Z"
    },
    {
      "node_id": "node2",
      "last_mode": "AUTO",
      "last_updated": "2024-01-15T10:35:00Z",
      "is_alive": true,
      "last_heartbeat": "2024-01-15T10:35:00Z"
    }
  ]
}
```

### Health Check

```bash
curl http://localhost:8080/health
```

Response:
```json
{
  "status": "healthy",
  "state": "LEADER"
}
```

### Bootstrap Cluster via API

If you need to bootstrap a node as leader via API after it has already started:

```bash
curl -X POST http://localhost:8080/api/cluster/bootstrap
```

Response:
```json
{
  "success": true,
  "message": "node bootstrapped as cluster leader",
  "node_id": "node1"
}
```

### Add Peer to Cluster (Leader only)

Leader can add new peers dynamically:

```bash
curl -X POST http://localhost:8080/api/cluster/peers \
  -H "Content-Type: application/json" \
  -d '{
    "node_id": "node4",
    "raft_addr": "server4:5000"
  }'
```

### List Cluster Peers

```bash
curl http://localhost:8080/api/cluster/peers
```

Response:
```json
{
  "node_id": "node1",
  "state": "LEADER",
  "leader": "node1",
  "peers": {
    "node1": "localhost:5000",
    "node2": "localhost:5010",
    "node3": "localhost:5020"
  }
}
```

## gRPC API

The service also exposes a gRPC interface defined in `pkg/pb/timectl.proto`:

```protobuf
service TimeCtl {
    rpc GetTimeMode(GetTimeModeRequest) returns (GetTimeModeResponse);
    rpc SetTimeMode(SetTimeModeRequest) returns (SetTimeModeResponse);
    rpc SyncState(SyncStateRequest) returns (SyncStateResponse);
}
```

## Operating Modes


## NTP Apply Command Behavior

- When a new cluster time mode decision is applied, timectl runs the command specified by `--ntp-apply-cmd` (or `TIMECTL_NTP_APPLY_CMD`).
- The command receives the current mode, order ID, and (if set) manual time as environment variables.
- No NTP/ntpd service is started or stopped by timectl itself.
- If no command is set, nothing is run.

### AUTO Mode (Default)

- System synchronizes time from configured external NTP sources
- No operator intervention required
- Automatic failover to manual mode if external NTP becomes unavailable
- Achieved through consensus when majority of nodes are in AUTO mode

### MANUAL Mode

- Operator provides time through the API
- External NTP synchronization is disabled
- Useful when external time sources are not available
- All cluster nodes apply the same manual time

## Consensus Algorithm

1. **State Collection**: Each node periodically reports its current time mode
2. **Voting**: When a node proposes a mode change, all nodes vote
3. **Decision**: Majority vote determines the new cluster-wide time mode
4. **Application**: Once consensus is reached, all nodes start `ntpd` and then apply the mode
5. **Verification**: Periodic health checks ensure consistency

## Cluster Operations

### Starting a 3-Node Cluster

All nodes have the same role - they automatically bootstrap or join based on configuration:

```bash
#!/bin/bash

# Kill any existing processes
pkill -f "timectl" || true
sleep 1

# Clean up old data
rm -rf ./data

# Start node 1 (will bootstrap as leader since no --join specified)
./bin/timectl \
  --node-id node1 \
  --raft-addr 0.0.0.0:5000 \
  --rpc-addr 0.0.0.0:5001 \
  --http-addr 0.0.0.0:8080 \
  --data-dir ./data/node1 \
  -v &
sleep 2

# Start node 2 (joins node1)
./bin/timectl \
  --node-id node2 \
  --raft-addr 0.0.0.0:5010 \
  --rpc-addr 0.0.0.0:5011 \
  --http-addr 0.0.0.0:8081 \
  --data-dir ./data/node2 \
  --join localhost:5000 \
  -v &
sleep 1

# Start node 3 (joins node1)
./bin/timectl \
  --node-id node3 \
  --raft-addr 0.0.0.0:5020 \
  --rpc-addr 0.0.0.0:5021 \
  --http-addr 0.0.0.0:8082 \
  --data-dir ./data/node3 \
  --join localhost:5000 \
  -v &

echo "Cluster started. Check logs above."
```

### Scaling to 6 Nodes (Production)

For a production 6-node cluster, all nodes use the same symmetric model. Start the first node without `--join`, then all others with `--join`:

```bash
# Start node1 (bootstraps automatically)
./bin/timectl --node-id node1 --raft-addr server1.local:5000 \
  --http-addr 0.0.0.0:8080 -v &

# Join remaining nodes
for i in {2..6}; do
  ./bin/timectl --node-id node$i \
    --raft-addr server$i.local:5000 \
    --http-addr 0.0.0.0:8080 \
    --join server1.local:5000 -v &
done
```

## Troubleshooting

### Cluster not reaching quorum
- Ensure at least 3 nodes are running
- Check network connectivity between nodes
- Verify correct Raft addresses are configured

### Time not synchronizing
- Check NTP service is running: `systemctl status ntp`
- Verify NTP server addresses are reachable
- Check system has required permissions to set time

### Leader election failures
- Increase election timeout with config adjustment
- Check network stability
- Review firewall rules allowing Raft communication

## System Requirements

- **Go**: 1.22 or later
- **OS**: Windows, Linux, macOS
- **Network**: Low-latency network for Raft consensus
- **Permissions**: Administrative access to manage NTP service
- **Disk**: Minimum 100MB for state storage

## Windows Specific Notes

- NTP service: `W32Time` (Windows Time Service)
- Requires elevated privileges to start/stop service
- Use `w32tm` command for time management
- Firewall rules must allow port 5000 (default Raft port)

## Linux Specific Notes

- NTP service: `ntp`, `ntpd`, or `chronyd` depending on distribution
- May require `sudo` for time management
- Use `systemctl` to manage services
- Ensure `ntpq` is installed for NTP queries

## License

MIT License

## Contributing

Contributions welcome. Please ensure:
- Code follows Go conventions
- Tests pass: `go test ./...`
- No breaking changes to API without versioning