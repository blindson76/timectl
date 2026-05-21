#!/bin/bash
# API test script for timectl

API_URL="${1:-http://localhost:8080}"

echo "timectl - API Test Script"
echo "========================="
echo "Using API URL: $API_URL"
echo ""

# Test 1: Health check
echo "1. Health Check"
echo "   curl $API_URL/health"
curl -s "$API_URL/health" | jq .
echo ""

# Test 2: Get status
echo "2. Get Cluster Status"
echo "   curl $API_URL/api/status"
curl -s "$API_URL/api/status" | jq .
echo ""

# Test 3: Get current time mode
echo "3. Get Current Time Mode"
echo "   curl $API_URL/api/timemode"
curl -s "$API_URL/api/timemode" | jq .
echo ""

# Test 4: Get cluster info
echo "4. Get Cluster Info"
echo "   curl $API_URL/api/cluster"
curl -s "$API_URL/api/cluster" | jq .
echo ""

# Test 5: Get server states
echo "5. Get Server States"
echo "   curl $API_URL/api/servers"
curl -s "$API_URL/api/servers" | jq .
echo ""

# Test 6: Try to set time mode (will work only on leader)
echo "6. Try to Set Time Mode to MANUAL"
echo "   curl -X POST $API_URL/api/timemode \\"
echo "     -H 'Content-Type: application/json' \\"
echo "     -d '{\"mode\":\"MANUAL\",\"operator_id\":\"test\",\"manual_time\":\"2024-01-15T10:30:00Z\"}'"
curl -s -X POST "$API_URL/api/timemode" \
  -H "Content-Type: application/json" \
  -d '{"mode":"MANUAL","operator_id":"test","manual_time":"2024-01-15T10:30:00Z"}' | jq .
echo ""

# Test 7: Check mode was changed
echo "7. Verify Time Mode Changed"
echo "   curl $API_URL/api/timemode"
sleep 1
curl -s "$API_URL/api/timemode" | jq .
echo ""

# Test 8: Switch back to AUTO
echo "8. Set Time Mode Back to AUTO"
echo "   curl -X POST $API_URL/api/timemode \\"
echo "     -H 'Content-Type: application/json' \\"
echo "     -d '{\"mode\":\"AUTO\",\"operator_id\":\"test\"}'"
curl -s -X POST "$API_URL/api/timemode" \
  -H "Content-Type: application/json" \
  -d '{"mode":"AUTO","operator_id":"test"}' | jq .
echo ""

echo "========================="
echo "API tests completed"
