#!/bin/bash

BASE_URL="http://localhost:8124"

# Check if wrk is installed
if ! command -v wrk >/dev/null 2>&1; then
    echo "Error: wrk not found in PATH. Please install wrk first." >&2
    exit 1
fi

# Test parameters: lightweight configuration
WRK_PARAMS="-t 2 -c 5 -d 5s -T 10s"

echo ""
echo "========================================================================================================="
echo "                          ETH RPC Stress Test with Real Parameters"
echo "========================================================================================================="
echo "Test Node: $BASE_URL"
echo "Test Time: $(date '+%Y-%m-%d %H:%M:%S')"
echo "Test Config: 2 threads, 5 connections, 5 seconds duration"
echo "---------------------------------------------------------------------------------------------------------"
printf "%-40s | %12s | %12s | %12s | %-15s\n" "Method" "QPS" "Avg(ms)" "Max(ms)" "Status"
echo "========================================================================================================="

# Statistics variables
total_qps=0
total_methods=0
success_methods=0
failed_methods=0

# Convert latency unit to milliseconds
convert_to_ms() {
    local value=$1
    if [[ $value == *"ms" ]]; then
        echo $value | sed 's/ms//'
    elif [[ $value == *"us" ]]; then
        echo $value | sed 's/us//' | awk '{printf "%.2f", $1/1000}'
    elif [[ $value == *"s" ]]; then
        echo $value | sed 's/s//' | awk '{printf "%.2f", $1*1000}'
    else
        echo "N/A"
    fi
}

# Test methods with parameters
test_method_with_params() {
    local method=$1
    local params=$2
    local description=$3
    
    # Create temporary Lua script
    cat > /tmp/test_${method}.lua << EOF
dofile("common.lua")
methodName = "$method"
wrk.method = "POST"
wrk.headers["Content-Type"] = "application/json"

threads = {}
counter = 1

setup = function(thread)
    initialize(thread, threads, counter)
end

request = function()
    local body = '{"jsonrpc":"2.0","method":"$method","params":$params,"id":1}'
    headers = {}
    headers["Content-Type"] = "application/json"
    return wrk.format("POST", nil, headers, body)
end

response = handle_response

done = function(summary, latency, requests)
    print_summary(summary, latency, requests, threads, methodName)
end
EOF
    
    # Run test
    output=$(wrk $WRK_PARAMS -s /tmp/test_${method}.lua "$BASE_URL" 2>&1)
    
    # Extract QPS
    qps=$(echo "$output" | grep "Requests/sec:" | awk '{printf "%.2f", $2}')
    
    # Extract average latency
    avg_latency=$(echo "$output" | grep "Latency" | awk '{print $2}' | head -1)
    
    # Extract max latency
    max_latency=$(echo "$output" | grep "Latency" | awk '{print $4}' | head -1)
    
    avg_ms=$(convert_to_ms "$avg_latency")
    max_ms=$(convert_to_ms "$max_latency")
    
    # Check test status
    if [ -z "$qps" ] || [ "$qps" == "0" ] || [ "$qps" == "0.00" ]; then
        status="❌ Failed"
        qps="0"
        ((failed_methods++))
    else
        status="✅ Success"
        ((success_methods++))
        total_qps=$(echo "$total_qps + $qps" | bc)
    fi
    
    ((total_methods++))
    
    # Print results
    printf "%-40s | %12s | %12s | %12s | %-15s\n" "$method" "$qps" "$avg_ms" "$max_ms" "$status"
    
    # Clean up
    rm -f /tmp/test_${method}.lua
}

echo ""
echo "1. Getting real data from the node..."
echo "---------------------------------------------------------------------------------------------------------"

# Get real transaction hash from block 1
TX_HASH=$(curl -s -X POST $BASE_URL -H "Content-Type: application/json" \
    -d '{"jsonrpc":"2.0","method":"eth_getBlockByNumber","params":["0x1", true],"id":1}' \
    | jq -r '.result.transactions[0].hash')
echo "Found transaction hash: $TX_HASH"

# Get real block hash
BLOCK_HASH=$(curl -s -X POST $BASE_URL -H "Content-Type: application/json" \
    -d '{"jsonrpc":"2.0","method":"eth_getBlockByNumber","params":["0x100", false],"id":1}' \
    | jq -r '.result.hash')
echo "Found block hash: $BLOCK_HASH"

# Get a real address (use coinbase from block 1)
ADDRESS=$(curl -s -X POST $BASE_URL -H "Content-Type: application/json" \
    -d '{"jsonrpc":"2.0","method":"eth_getBlockByNumber","params":["0x1", false],"id":1}' \
    | jq -r '.result.miner')
echo "Found address: $ADDRESS"

# Get latest block number
LATEST_BLOCK=$(curl -s -X POST $BASE_URL -H "Content-Type: application/json" \
    -d '{"jsonrpc":"2.0","method":"eth_blockNumber","params":[],"id":1}' \
    | jq -r '.result')
echo "Latest block: $LATEST_BLOCK"

echo ""
echo "2. Testing ETH methods with real parameters..."
echo "========================================================================================================="

# 1. Transaction-related methods
test_method_with_params "eth_getTransactionByHash" "[\"$TX_HASH\"]" "Get transaction by hash"
test_method_with_params "eth_getTransactionReceipt" "[\"$TX_HASH\"]" "Get transaction receipt"
test_method_with_params "eth_getRawTransactionByHash" "[\"$TX_HASH\"]" "Get raw transaction"

# 2. Block-related methods with hash
test_method_with_params "eth_getBlockByHash" "[\"$BLOCK_HASH\", false]" "Get block by hash"
test_method_with_params "eth_getBlockTransactionCountByHash" "[\"$BLOCK_HASH\"]" "Get tx count by block hash"
test_method_with_params "eth_getUncleCountByBlockHash" "[\"$BLOCK_HASH\"]" "Get uncle count by hash"

# 3. Block-related methods with number
test_method_with_params "eth_getBlockByNumber" "[\"0x100\", false]" "Get block by number"
test_method_with_params "eth_getBlockTransactionCountByNumber" "[\"0x100\"]" "Get tx count by number"
test_method_with_params "eth_getUncleCountByBlockNumber" "[\"0x100\"]" "Get uncle count by number"
test_method_with_params "eth_getBlockReceipts" "[\"0x100\"]" "Get block receipts"

# 4. Transaction by index
test_method_with_params "eth_getTransactionByBlockHashAndIndex" "[\"$BLOCK_HASH\", \"0x0\"]" "Get tx by hash and index"
test_method_with_params "eth_getTransactionByBlockNumberAndIndex" "[\"0x1\", \"0x0\"]" "Get tx by number and index"
test_method_with_params "eth_getRawTransactionByBlockHashAndIndex" "[\"$BLOCK_HASH\", \"0x0\"]" "Get raw tx by hash/index"
test_method_with_params "eth_getRawTransactionByBlockNumberAndIndex" "[\"0x1\", \"0x0\"]" "Get raw tx by number/index"

# 5. Uncle methods
test_method_with_params "eth_getUncleByBlockHashAndIndex" "[\"$BLOCK_HASH\", \"0x0\"]" "Get uncle by hash/index"
test_method_with_params "eth_getUncleByBlockNumberAndIndex" "[\"0x100\", \"0x0\"]" "Get uncle by number/index"

# 6. Account-related methods
test_method_with_params "eth_getBalance" "[\"$ADDRESS\", \"latest\"]" "Get balance"
test_method_with_params "eth_getTransactionCount" "[\"$ADDRESS\", \"latest\"]" "Get transaction count"
test_method_with_params "eth_getCode" "[\"$ADDRESS\", \"latest\"]" "Get code"
test_method_with_params "eth_getStorageAt" "[\"$ADDRESS\", \"0x0\", \"latest\"]" "Get storage at position"

# 7. Filter methods
test_method_with_params "eth_newFilter" "[{\"fromBlock\":\"0x1\",\"toBlock\":\"0x2\"}]" "Create new filter"
test_method_with_params "eth_newBlockFilter" "[]" "Create block filter"
test_method_with_params "eth_newPendingTransactionFilter" "[]" "Create pending tx filter"
test_method_with_params "eth_uninstallFilter" "[\"0x1\"]" "Uninstall filter"
test_method_with_params "eth_getFilterChanges" "[\"0x1\"]" "Get filter changes"
test_method_with_params "eth_getFilterLogs" "[\"0x1\"]" "Get filter logs"
test_method_with_params "eth_getLogs" "[{\"fromBlock\":\"0x1\",\"toBlock\":\"0x2\"}]" "Get logs"

# 8. Call and estimate methods
test_method_with_params "eth_call" "[{\"to\":\"$ADDRESS\",\"data\":\"0x\"}, \"latest\"]" "Call contract"
test_method_with_params "eth_estimateGas" "[{\"from\":\"$ADDRESS\",\"to\":\"$ADDRESS\",\"value\":\"0x0\"}]" "Estimate gas"
test_method_with_params "eth_createAccessList" "[{\"from\":\"$ADDRESS\",\"to\":\"$ADDRESS\",\"data\":\"0x\"}]" "Create access list"

# 9. Proof method
test_method_with_params "eth_getProof" "[\"$ADDRESS\", [\"0x0\"], \"latest\"]" "Get merkle proof"

# 10. Simple query methods (no parameters or simple params)
test_method_with_params "eth_blockNumber" "[]" "Get block number"
test_method_with_params "eth_chainId" "[]" "Get chain ID"
test_method_with_params "eth_syncing" "[]" "Get syncing status"
test_method_with_params "eth_gasPrice" "[]" "Get gas price"
test_method_with_params "eth_protocolVersion" "[]" "Get protocol version"
test_method_with_params "eth_accounts" "[]" "Get accounts"

# 11. Send transaction (with dummy signed tx)
# Note: This will likely fail but we test it anyway
DUMMY_TX="0xf86b0185098bca5a00825208940000000000000000000000000000000000000000880de0b6b3a76400008025a04f"
test_method_with_params "eth_sendRawTransaction" "[\"$DUMMY_TX\"]" "Send raw transaction"

echo "========================================================================================================="
echo ""
echo "Test Summary:"
echo "---------------------------------------------------------------------------------------------------------"
echo "Total methods tested: $total_methods"
echo "Successful methods: $success_methods"
echo "Failed methods: $failed_methods"

if [ $success_methods -gt 0 ]; then
    avg_qps=$(echo "scale=2; $total_qps / $success_methods" | bc)
    echo "Average QPS: $avg_qps"
fi

echo "========================================================================================================="
echo "Test completed at: $(date '+%Y-%m-%d %H:%M:%S')"
echo "========================================================================================================="
