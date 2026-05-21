# timectl - Symmetric Node Architecture

## Overview

All nodes in a timectl cluster operate with **identical roles**. There is no special "first node" or "bootstrap node" designation. Each node operates symmetrically and can independently handle cluster initialization or joining.

## Key Principles

### 1. **No Distinguished First Node**
- All 6 servers have exactly the same role and capabilities
- No node is administratively special or privileged
- Leadership is determined purely through Raft consensus

### 2. **Automatic Bootstrapping**
- First node to start (with no `--join` parameter) automatically bootstraps the cluster
- No explicit `--bootstrap` flag needed
- Subsequent nodes detect existing cluster and join automatically

### 3. **Symmetric Configuration**
- All nodes run with identical binary and configuration structure
- Only difference: `--join` parameter on subsequent nodes
- Configuration parameters are the same across all nodes

### 4. **Dynamic Node Management**
- Nodes can be added/removed from the cluster dynamically
- No reconfiguration of existing nodes needed
- Leader election is automatic and continuous

## Deployment Model

### Starting the Cluster

**Step 1: Start First Node** (automatically becomes leader)
```bash
./timectl --node-id node1 --raft-addr server1:5000 --http-addr 0.0.0.0:8080
```

**Step 2-6: Start Other Nodes** (automatically join cluster)
```bash
./timectl --node-id node2 --raft-addr server2:5000 --http-addr 0.0.0.0:8080 --join server1:5000
```

All nodes use identical command structure and binary.

## Advantages of Symmetric Architecture

1. **Simplicity**: All nodes are identical - easier to manage and understand
2. **Resilience**: No single point of configuration failure
3. **Scalability**: Easy to add/remove nodes without special procedures
4. **Symmetry**: True distributed system where all nodes are peers
5. **Failover**: If leader fails, new leader elected from identical nodes
6. **Testing**: Can test cluster behavior without special first-node setup

## Leader Election

- Raft consensus algorithm determines the leader
- Leader is elected from the current set of nodes
- If leader fails, a new leader is elected automatically
- All nodes are equally eligible to become leader

## Quorum Requirements

- Minimum 3 nodes required for cluster quorum
- Majority voting determines consensus
- 6-node cluster: 4 nodes required for decision

## State Initialization

### Fresh Node (No Prior State)
```
Node starts
  ↓
Check for existing state in data directory
  ↓
State found? YES → Join as new follower
             NO → Is --join specified? YES → Connect to cluster
                                       NO → Bootstrap as leader
```

### Recovering Node (Has Prior State)
```
Node starts
  ↓
State found in data directory
  ↓
Resume from saved Raft state
  ↓
Rejoin cluster
```

## Operational Tasks

### Add a New Node
```bash
# Start new node
./timectl --node-id node7 --raft-addr server7:5000 --join server1:5000

# That's it! Node automatically joins and participates in consensus
```

### Remove a Node
```bash
# API call on leader to remove node
curl -X POST http://leader:8080/api/cluster/peers \
  -d '{"action":"remove","node_id":"node7"}'
```

### Recover from Node Failure
1. Replace the failed node hardware/OS
2. Start the new node with same configuration:
   ```bash
   ./timectl --node-id node1 --raft-addr server1:5000 --join server2:5000
   ```
3. Node automatically syncs state and joins cluster

## Network Topology

```
┌──────────────────────────────────────────────────────────────┐
│                  Raft Consensus Cluster                       │
│                    (All nodes equal)                          │
├──────────┬──────────┬──────────┬──────────┬──────────┬────────┤
│  Node 1  │  Node 2  │  Node 3  │  Node 4  │  Node 5  │ Node 6 │
│  Port    │  Port    │  Port    │  Port    │  Port    │ Port   │
│  5000    │  5010    │  5020    │  5030    │  5040    │ 5050   │
└────┬─────┴────┬─────┴────┬─────┴────┬─────┴────┬─────┴────┬───┘
     └──────────┴──────────┴──────────┴──────────┴──────────┘
          Gossip + Consensus Communication
          All nodes communicate with all nodes
```

## Configuration Parameters

### Required
- `--node-id`: Unique identifier for this node

### Cluster (Identical on all nodes)
- `--raft-addr`: Where this node listens for Raft cluster traffic
- `--rpc-addr`: Where this node listens for gRPC traffic
- `--http-addr`: Where this node listens for HTTP API

### Joining (Varies by node)
- `--join`: Address of an existing cluster member (only for nodes 2-N)
  - Node 1: Omit this parameter (bootstraps as leader)
  - Nodes 2-6: Include this parameter pointing to any existing node

### System (Same on all nodes)
- `--data-dir`: Where to store Raft state
- `--ntp-servers`: NTP sources to use
- `--ntp`: Whether to manage NTP

## Example: Full 6-Node Deployment

```bash
# Server 1 (Bootstrap node - only one that doesn't need --join)
./timectl --node-id node1 \
  --raft-addr server1.local:5000 \
  --http-addr 0.0.0.0:8080 \
  --data-dir /opt/timectl/data &

sleep 2

# Servers 2-6 (All identical except --node-id and --raft-addr)
for i in {2..6}; do
  ./timectl --node-id node$i \
    --raft-addr server$i.local:5000 \
    --http-addr 0.0.0.0:8080 \
    --data-dir /opt/timectl/data \
    --join server1.local:5000 &
  sleep 1
done
```

## State Management

### Persistent State
- Each node maintains copy of cluster state in local BoltDB
- All nodes have identical state after applying consensus changes
- State is durable and survives node restarts

### State Sync
- New nodes automatically sync full state when joining
- Raft handles state replication automatically
- No manual state transfer needed

## Monitoring

All nodes expose same monitoring endpoints:

```bash
# Check status on any node
curl http://any-node:8080/api/status

# Check cluster members
curl http://any-node:8080/api/cluster/peers

# Check time mode on any node
curl http://any-node:8080/api/timemode
```

## Consensus Decision Making

When determining time mode:
1. Each node maintains its preferred mode
2. New mode change is proposed to cluster
3. Nodes vote through Raft consensus
4. Decision applies to all nodes immediately
5. Decision is durable and survives failures

Since all nodes have symmetric roles in voting, any node can propose changes and all participate equally in consensus.

## Migration and Maintenance

### Rolling Restart
```bash
# Safe to restart nodes one at a time
for i in {1..6}; do
  systemctl restart timectl-node$i
  sleep 5  # Wait for node to rejoin
done
```

### Upgrade
1. Build new binary
2. Replace binary on first node and restart
3. Other nodes detect and follow automatically
4. No special upgrade sequence needed

### Scale from 3 to 6 Nodes
```bash
# Simply start new nodes with --join parameter
for i in {4..6}; do
  ./timectl --node-id node$i \
    --raft-addr server$i.local:5000 \
    --join server1.local:5000 &
done
```

## Design Benefits

1. **Operational Simplicity**: Everyone learns one pattern - all nodes are the same
2. **High Availability**: No single point of administrative failure
3. **Fault Tolerance**: Any 4 of 6 nodes can form quorum
4. **Consistency**: Raft guarantees all nodes have same state
5. **Predictability**: Behavior is deterministic and symmetric

## Comparison: Bootstrap vs. Symmetric Model

| Aspect | Traditional Bootstrap | Symmetric (timectl) |
|--------|----------------------|---------------------|
| First Node | Special `--bootstrap` flag | No flag, natural bootstrap |
| Node Roles | Different | All identical |
| Learning Curve | Higher | Lower |
| Operational Pattern | Node 1 ≠ Nodes 2-N | All nodes same |
| Failure Handling | Special procedures | Standard procedures |
| Recovery | May be different | Identical process |
