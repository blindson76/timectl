# timectl Deployment Guide

This guide covers deploying timectl in a production environment with 6 servers.

## Prerequisites

- 6 Windows Server or Linux servers (2016 or later for Windows)
- Administrative/root access on all servers
- Network connectivity between all servers (low latency recommended)
- Go 1.22+ installed on build machine
- Port 5000-5020 available for Raft cluster communication
- Port 5001-5021 available for gRPC communication
- Port 8080-8082 available for HTTP API (can be on same port)

## Architecture Overview

```
┌─────────────────────────────────────────────────────────────┐
│                  6-Node timectl Cluster                      │
├──────────┬──────────┬──────────┬──────────┬──────────┬────────┤
│ Node 1   │ Node 2   │ Node 3   │ Node 4   │ Node 5   │ Node 6 │
│ (Leader) │          │          │          │          │        │
└─────┬────┴────┬─────┴────┬─────┴────┬─────┴────┬─────┴────┬───┘
      │         │          │          │          │          │
      └─────────┴──────────┴──────────┴──────────┴──────────┘
         Raft Consensus Cluster (5000-5020)
```

## Build Instructions

### 1. Build on Build Machine

```bash
cd /workspaces/timectl

# Generate protobuf files
make proto

# Build for target platform
# For Linux
make build-all

# Single platform
make build
```

## Deployment Steps

### 1. Prepare Servers

On each server, create a dedicated user and directory:

**Linux:**
```bash
sudo useradd -m -s /bin/bash timectl
sudo mkdir -p /opt/timectl/{bin,data,logs}
sudo chown -R timectl:timectl /opt/timectl
```

**Windows (PowerShell as Administrator):**
```powershell
New-Item -ItemType Directory -Path "C:\Program Files\timectl\data" -Force
New-Item -ItemType Directory -Path "C:\Program Files\timectl\logs" -Force
```

### 2. Copy Binary and Configuration

**Linux:**
```bash
scp bin/timectl-linux-amd64 user@server1:/opt/timectl/bin/timectl
sudo chmod +x /opt/timectl/bin/timectl
```

**Windows:**
```powershell
Copy-Item bin\timectl-windows-amd64.exe "C:\Program Files\timectl\timectl.exe"
```

### 3. Configure Firewall

**Linux (UFW):**
```bash
sudo ufw allow 5000/tcp  # Raft
sudo ufw allow 5001/tcp  # gRPC
sudo ufw allow 8080/tcp  # HTTP API
```

**Windows (PowerShell as Administrator):**
```powershell
New-NetFirewallRule -DisplayName "timectl-raft" -Direction Inbound -Action Allow -Protocol TCP -LocalPort 5000
New-NetFirewallRule -DisplayName "timectl-grpc" -Direction Inbound -Action Allow -Protocol TCP -LocalPort 5001
New-NetFirewallRule -DisplayName "timectl-http" -Direction Inbound -Action Allow -Protocol TCP -LocalPort 8080
```

### 4. Create Systemd Service (Linux)

All nodes use the same service template with different configuration. The key difference is the `--join` parameter:

**First Node** (`/etc/systemd/system/timectl.service` on node1):

```ini
[Unit]
Description=timectl - Distributed Time Control System
After=network.target

[Service]
Type=simple
User=timectl
Group=timectl
WorkingDirectory=/opt/timectl
ExecStart=/opt/timectl/bin/timectl \
  --node-id=node1 \
  --raft-addr=0.0.0.0:5000 \
  --rpc-addr=0.0.0.0:5001 \
  --http-addr=0.0.0.0:8080 \
  --data-dir=/opt/timectl/data \
  --ntp-servers="0.pool.ntp.org,1.pool.ntp.org" \
  -v

Restart=on-failure
RestartSec=10
StandardOutput=journal
StandardError=journal
SyslogIdentifier=timectl
PrivateTmp=yes
NoNewPrivileges=true

[Install]
WantedBy=multi-user.target
```

**Other Nodes** (node2-node6, with `--join` parameter):

```ini
[Unit]
Description=timectl - Distributed Time Control System
After=network.target

[Service]
Type=simple
User=timectl
Group=timectl
WorkingDirectory=/opt/timectl
ExecStart=/opt/timectl/bin/timectl \
  --node-id=node2 \
  --raft-addr=0.0.0.0:5000 \
  --rpc-addr=0.0.0.0:5001 \
  --http-addr=0.0.0.0:8080 \
  --data-dir=/opt/timectl/data \
  --join=server1.local:5000 \
  --ntp-servers="0.pool.ntp.org,1.pool.ntp.org" \
  -v

Restart=on-failure
RestartSec=10
StandardOutput=journal
StandardError=journal
SyslogIdentifier=timectl
PrivateTmp=yes
NoNewPrivileges=true

[Install]
WantedBy=multi-user.target
```

Enable and start:
```bash
sudo systemctl daemon-reload
sudo systemctl enable timectl
sudo systemctl start timectl
```

### 5. Create Windows Service

Create a PowerShell script `C:\Program Files\timectl\install-service.ps1`:

```powershell
$serviceName = "timectl"
$binPath = "C:\Program Files\timectl\timectl.exe --node-id=node1 --raft-addr=0.0.0.0:5000 --rpc-addr=0.0.0.0:5001 --http-addr=0.0.0.0:8080 --data-dir=C:\Program Files\timectl\data --join=server2.local:5000 -v"

# Create service
New-Service -Name $serviceName -BinaryPathName $binPath -DisplayName "timectl - Time Control Service" -StartupType Automatic

# Start service
Start-Service -Name $serviceName

# Check status
Get-Service -Name $serviceName
```

Run as Administrator:
```powershell
PowerShell -ExecutionPolicy Bypass -File "C:\Program Files\timectl\install-service.ps1"
```

### 6. Configure Each Node

All nodes have identical symmetric roles. The only difference is the `--join` parameter:

**Node 1 (First Node - No Join):**

This node will automatically bootstrap as the cluster leader since no `--join` is specified:

```bash
--node-id node1 \
--raft-addr server1.local:5000 \
--rpc-addr server1.local:5001 \
--http-addr 0.0.0.0:8080 \
--data-dir /opt/timectl/data \
--ntp-servers "0.pool.ntp.org,1.pool.ntp.org"
```

**Nodes 2-6 (Join Cluster):**

All other nodes use the same configuration with `--join` parameter pointing to any existing node:

```bash
--node-id node2 \
--raft-addr server2.local:5000 \
--rpc-addr server2.local:5001 \
--http-addr 0.0.0.0:8080 \
--data-dir /opt/timectl/data \
--join server1.local:5000 \
--ntp-servers "0.pool.ntp.org,1.pool.ntp.org"
```

All nodes operate symmetrically - no special designation as "leader" in configuration. Leadership is determined through Raft consensus.

### 7. Verify Deployment

Check cluster status from any node:
```bash
curl http://localhost:8080/api/status | jq .

# Expected output when all 6 nodes are running:
# {
#   "cluster_size": 6,
#   "has_quorum": true,
#   "state": "LEADER" (or "FOLLOWER"),
#   ...
# }
```

## Production Checklist

- [ ] All 6 nodes can reach each other on port 5000 (Raft)
- [ ] All 6 nodes are reporting as alive
- [ ] Cluster quorum is achieved (6/6 nodes)
- [ ] Leader is elected and stable
- [ ] HTTP API is responding on all nodes
- [ ] NTP service is managing time properly
- [ ] Logs are being written correctly
- [ ] Services auto-start after reboot
- [ ] Firewall rules are in place
- [ ] Monitoring/alerting is configured (optional)

## Monitoring and Troubleshooting

### Check Cluster Status
```bash
# From any node
curl http://localhost:8080/api/status | jq .

# Check server states
curl http://localhost:8080/api/servers | jq .
```

### View Logs

**Linux:**
```bash
# Systemd journal
sudo journalctl -u timectl -f

# Or check service status
sudo systemctl status timectl
```

**Windows:**
```powershell
# Event Viewer
Get-EventLog -LogName Application -Source timectl -Newest 10

# Or service status
Get-Service -Name timectl
```

### Common Issues

**Cluster not reaching quorum:**
- Verify all 6 nodes are running: `curl http://localhost:8080/api/cluster`
- Check network connectivity between nodes
- Check firewall rules on all nodes

**NTP not synchronizing:**
- Verify NTP service is running
- Check system logs for NTP errors
- Verify NTP servers are reachable

**Leader election stuck:**
- Check clock drift on all nodes
- Verify network latency is reasonable (<100ms recommended)
- Check Raft election timeout settings

## Scaling

### Adding More Nodes
```bash
# On new node
./timectl --node-id=node7 --join=server1.local:5000 ...
```

### Removing a Node
```bash
# From any node - API to remove a node (to be implemented)
curl -X POST http://leader:8080/api/cluster/remove \
  -d '{"node_id":"node7"}'
```

## Performance Tuning

For large clusters or high-latency networks, adjust timeouts:

```bash
# In configuration - add these options
--election-timeout=2s \
--heartbeat-timeout=200ms \
```

## Backup and Recovery

### Backup State
```bash
# Backup Raft database on each node
cp -r /opt/timectl/data /backup/timectl-data-backup-$(date +%Y%m%d)
```

### Restore from Backup
```bash
# Stop timectl service
sudo systemctl stop timectl

# Restore data
rm -rf /opt/timectl/data
cp -r /backup/timectl-data-backup-20240115 /opt/timectl/data

# Start timectl service
sudo systemctl start timectl
```

## Docker Deployment (Optional)

Create `Dockerfile`:
```dockerfile
FROM golang:1.22 AS builder
WORKDIR /app
COPY . .
RUN go build -o /app/timectl ./cmd/timectl

FROM ubuntu:24.04
RUN apt-get update && apt-get install -y ntp chrony
COPY --from=builder /app/timectl /usr/local/bin/
EXPOSE 5000 5001 8080
ENTRYPOINT ["timectl"]
```

Build and run:
```bash
docker build -t timectl:latest .

docker run -d \
  --name timectl-node1 \
  -p 5000:5000 \
  -p 5001:5001 \
  -p 8080:8080 \
  -v /opt/timectl/data:/data \
  timectl:latest \
  --node-id=node1 \
  --raft-addr=0.0.0.0:5000 \
  --rpc-addr=0.0.0.0:5001 \
  --http-addr=0.0.0.0:8080 \
  --data-dir=/data \
  --bootstrap
```

## Support

For issues or questions:
1. Check logs: `journalctl -u timectl -f`
2. Verify cluster status: `curl http://localhost:8080/api/status`
3. Check API endpoints: `curl http://localhost:8080/health`
4. Review configuration: Ensure all nodes have correct join addresses
