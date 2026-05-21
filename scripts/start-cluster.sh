#!/bin/bash
# Start a 3-node test cluster

set -e

echo "timectl - Starting 3-Node Test Cluster"
echo "======================================"

# Kill any existing processes
echo "Stopping any existing timectl processes..."
pkill -f "timectl" || true
sleep 1

# Clean up old data
echo "Cleaning up old data..."
rm -rf ./data

# Build if needed
if [ ! -f "./bin/timectl" ]; then
  echo "Building timectl..."
  make build
fi

# Start node 1 (will bootstrap as leader since no --join specified)
echo ""
echo "Starting Node 1 (will become leader)..."
./bin/timectl \
  --node-id node1 \
  --raft-addr localhost:5000 \
  --rpc-addr localhost:5001 \
  --http-addr localhost:8080 \
  --data-dir ./data/node1 \
  -v &
NODE1_PID=$!
sleep 2

# Start node 2
echo ""
echo "Starting Node 2..."
./bin/timectl \
  --node-id node2 \
  --raft-addr localhost:5010 \
  --rpc-addr localhost:5011 \
  --http-addr localhost:8081 \
  --data-dir ./data/node2 \
  --join localhost:5000 \
  -v &
NODE2_PID=$!
sleep 1

# Start node 3
echo ""
echo "Starting Node 3..."
./bin/timectl \
  --node-id node3 \
  --raft-addr localhost:5020 \
  --rpc-addr localhost:5021 \
  --http-addr localhost:8082 \
  --data-dir ./data/node3 \
  --join localhost:5000 \
  -v &
NODE3_PID=$!

echo ""
echo "======================================"
echo "Cluster started with 3 nodes"
echo "======================================"
echo ""
echo "Node 1 (Leader):"
echo "  Raft:  localhost:5000"
echo "  RPC:   localhost:5001"
echo "  HTTP:  http://localhost:8080"
echo ""
echo "Node 2:"
echo "  Raft:  localhost:5010"
echo "  RPC:   localhost:5011"
echo "  HTTP:  http://localhost:8081"
echo ""
echo "Node 3:"
echo "  Raft:  localhost:5020"
echo "  RPC:   localhost:5021"
echo "  HTTP:  http://localhost:8082"
echo ""
echo "Example commands:"
echo "  # Check cluster status"
echo "  curl http://localhost:8080/api/status"
echo ""
echo "  # Get current time mode"
echo "  curl http://localhost:8080/api/timemode"
echo ""
echo "  # Set time mode to MANUAL (leader only)"
echo "  curl -X POST http://localhost:8080/api/timemode \\"
echo "    -H 'Content-Type: application/json' \\"
echo "    -d '{\"mode\":\"MANUAL\",\"operator_id\":\"admin\",\"manual_time\":\"2024-01-15T10:30:00Z\"}'"
echo ""
echo "  # Set time mode to AUTO"
echo "  curl -X POST http://localhost:8080/api/timemode \\"
echo "    -H 'Content-Type: application/json' \\"
echo "    -d '{\"mode\":\"AUTO\",\"operator_id\":\"admin\"}'"
echo ""
echo "Press Ctrl+C to stop the cluster"
echo ""

# Wait for all processes
wait $NODE1_PID $NODE2_PID $NODE3_PID
