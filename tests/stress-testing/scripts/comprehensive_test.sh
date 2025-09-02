#!/bin/bash

BASE_URL="http://localhost:8124"

# Check if wrk is installed
if ! command -v wrk >/dev/null 2>&1; then
    echo "Error: wrk not found in PATH. Please install wrk first." >&2
    exit 1
fi

# Test parameters: lightweight configuration
WRK_PARAMS="-t 2 -c 5 -d 3s -T 10s"

echo ""
echo "========================================================================================================="
echo "                     XLayer RPC Comprehensive Stress Test (ETH + zkEVM)"
echo "========================================================================================================="
echo "Test Node: $BASE_URL"
echo "Test Time: $(date '+%Y-%m-%d %H:%M:%S')"
echo "Test Config: 2 threads, 5 connections, 3 seconds duration per method"
echo "---------------------------------------------------------------------------------------------------------"

# Statistics variables
total_qps=0
total_methods=0
success_methods=0
failed_methods=0
eth_total_qps=0
eth_methods=0
eth_success=0
zkevm_total_qps=0
zkevm_methods=0
zkevm_success=0

# Arrays to store results
declare -a all_results

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
    local category=$4  # ETH or zkEVM
    
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
        status="❌"
        qps="0"
        ((failed_methods++))
    else
        status="✅"
        ((success_methods++))
        total_qps=$(echo "$total_qps + $qps" | bc)
        
        # Update category statistics
        if [ "$category" == "ETH" ]; then
            ((eth_success++))
            eth_total_qps=$(echo "$eth_total_qps + $qps" | bc)
        else
            ((zkevm_success++))
            zkevm_total_qps=$(echo "$zkevm_total_qps + $qps" | bc)
        fi
    fi
    
    ((total_methods++))
    
    # Update category counters
    if [ "$category" == "ETH" ]; then
        ((eth_methods++))
    else
        ((zkevm_methods++))
    fi
    
    # Store result
    all_results+=("$category|$method|$qps|$avg_ms|$max_ms|$status")
    
    # Print results
    printf "[%-5s] %-40s | %10s | %8s | %8s | %s\n" "$category" "$method" "$qps" "$avg_ms" "$max_ms" "$status"
    
    # Clean up
    rm -f /tmp/test_${method}.lua
}

echo ""
echo "Phase 1: Getting real data from the node..."
echo "---------------------------------------------------------------------------------------------------------"

# Get real transaction hash from block 1
TX_HASH=$(curl -s -X POST $BASE_URL -H "Content-Type: application/json" \
    -d '{"jsonrpc":"2.0","method":"eth_getBlockByNumber","params":["0x1", true],"id":1}' \
    | jq -r '.result.transactions[0].hash')
echo "Transaction hash: ${TX_HASH:0:20}..."

# Get real block hash
BLOCK_HASH=$(curl -s -X POST $BASE_URL -H "Content-Type: application/json" \
    -d '{"jsonrpc":"2.0","method":"eth_getBlockByNumber","params":["0x100", false],"id":1}' \
    | jq -r '.result.hash')
echo "Block hash: ${BLOCK_HASH:0:20}..."

# Get a real address
ADDRESS=$(curl -s -X POST $BASE_URL -H "Content-Type: application/json" \
    -d '{"jsonrpc":"2.0","method":"eth_getBlockByNumber","params":["0x1", false],"id":1}' \
    | jq -r '.result.miner')
echo "Address: ${ADDRESS:0:20}..."

# Get latest block number
LATEST_BLOCK=$(curl -s -X POST $BASE_URL -H "Content-Type: application/json" \
    -d '{"jsonrpc":"2.0","method":"eth_blockNumber","params":[],"id":1}' \
    | jq -r '.result')
echo "Latest block: $LATEST_BLOCK"

# Get current batch number
BATCH_NUMBER=$(curl -s -X POST $BASE_URL -H "Content-Type: application/json" \
    -d '{"jsonrpc":"2.0","method":"zkevm_batchNumber","params":[],"id":1}' \
    | jq -r '.result')
echo "Current batch: $BATCH_NUMBER"

echo ""
echo "Phase 2: Testing all RPC methods..."
echo "========================================================================================================="
printf "[%-5s] %-40s | %10s | %8s | %8s | %s\n" "Type" "Method" "QPS" "Avg(ms)" "Max(ms)" "Status"
echo "========================================================================================================="

# ===========================
# ETH Standard Methods (38)
# ===========================
echo ""
echo "Testing ETH Standard Methods..."
echo "---------------------------------------------------------------------------------------------------------"

# 1. Simple query methods (no parameters)
test_method_with_params "eth_blockNumber" "[]" "Get block number" "ETH"
test_method_with_params "eth_chainId" "[]" "Get chain ID" "ETH"
test_method_with_params "eth_syncing" "[]" "Get syncing status" "ETH"
test_method_with_params "eth_gasPrice" "[]" "Get gas price" "ETH"
test_method_with_params "eth_protocolVersion" "[]" "Get protocol version" "ETH"
test_method_with_params "eth_accounts" "[]" "Get accounts" "ETH"

# 2. Transaction-related methods
test_method_with_params "eth_getTransactionByHash" "[\"$TX_HASH\"]" "Get tx by hash" "ETH"
test_method_with_params "eth_getTransactionReceipt" "[\"$TX_HASH\"]" "Get tx receipt" "ETH"
test_method_with_params "eth_getRawTransactionByHash" "[\"$TX_HASH\"]" "Get raw tx" "ETH"

# 3. Block methods with hash
test_method_with_params "eth_getBlockByHash" "[\"$BLOCK_HASH\", false]" "Get block by hash" "ETH"
test_method_with_params "eth_getBlockTransactionCountByHash" "[\"$BLOCK_HASH\"]" "Tx count by hash" "ETH"
test_method_with_params "eth_getUncleCountByBlockHash" "[\"$BLOCK_HASH\"]" "Uncle count by hash" "ETH"

# 4. Block methods with number
test_method_with_params "eth_getBlockByNumber" "[\"0x100\", false]" "Get block by number" "ETH"
test_method_with_params "eth_getBlockTransactionCountByNumber" "[\"0x100\"]" "Tx count by number" "ETH"
test_method_with_params "eth_getUncleCountByBlockNumber" "[\"0x100\"]" "Uncle count by number" "ETH"
test_method_with_params "eth_getBlockReceipts" "[\"0x100\"]" "Get block receipts" "ETH"

# 5. Transaction by index
test_method_with_params "eth_getTransactionByBlockHashAndIndex" "[\"$BLOCK_HASH\", \"0x0\"]" "Tx by hash/idx" "ETH"
test_method_with_params "eth_getTransactionByBlockNumberAndIndex" "[\"0x1\", \"0x0\"]" "Tx by num/idx" "ETH"
test_method_with_params "eth_getRawTransactionByBlockHashAndIndex" "[\"$BLOCK_HASH\", \"0x0\"]" "Raw tx hash/idx" "ETH"
test_method_with_params "eth_getRawTransactionByBlockNumberAndIndex" "[\"0x1\", \"0x0\"]" "Raw tx num/idx" "ETH"

# 6. Uncle methods
test_method_with_params "eth_getUncleByBlockHashAndIndex" "[\"$BLOCK_HASH\", \"0x0\"]" "Uncle by hash/idx" "ETH"
test_method_with_params "eth_getUncleByBlockNumberAndIndex" "[\"0x100\", \"0x0\"]" "Uncle by num/idx" "ETH"

# 7. Account methods
test_method_with_params "eth_getBalance" "[\"$ADDRESS\", \"latest\"]" "Get balance" "ETH"
test_method_with_params "eth_getTransactionCount" "[\"$ADDRESS\", \"latest\"]" "Get tx count" "ETH"
test_method_with_params "eth_getCode" "[\"$ADDRESS\", \"latest\"]" "Get code" "ETH"
test_method_with_params "eth_getStorageAt" "[\"$ADDRESS\", \"0x0\", \"latest\"]" "Get storage" "ETH"

# 8. Filter methods
test_method_with_params "eth_newFilter" "[{\"fromBlock\":\"0x1\",\"toBlock\":\"0x2\"}]" "New filter" "ETH"
test_method_with_params "eth_newBlockFilter" "[]" "New block filter" "ETH"
test_method_with_params "eth_newPendingTransactionFilter" "[]" "New pending filter" "ETH"
test_method_with_params "eth_uninstallFilter" "[\"0x1\"]" "Uninstall filter" "ETH"
test_method_with_params "eth_getFilterChanges" "[\"0x1\"]" "Filter changes" "ETH"
test_method_with_params "eth_getFilterLogs" "[\"0x1\"]" "Filter logs" "ETH"
test_method_with_params "eth_getLogs" "[{\"fromBlock\":\"0x1\",\"toBlock\":\"0x2\"}]" "Get logs" "ETH"

# 9. Call and estimate
test_method_with_params "eth_call" "[{\"to\":\"$ADDRESS\",\"data\":\"0x\"}, \"latest\"]" "Call contract" "ETH"
test_method_with_params "eth_estimateGas" "[{\"from\":\"$ADDRESS\",\"to\":\"$ADDRESS\",\"value\":\"0x0\"}]" "Estimate gas" "ETH"
test_method_with_params "eth_createAccessList" "[{\"from\":\"$ADDRESS\",\"to\":\"$ADDRESS\",\"data\":\"0x\"}]" "Access list" "ETH"

# 10. Proof
test_method_with_params "eth_getProof" "[\"$ADDRESS\", [\"0x0\"], \"latest\"]" "Get proof" "ETH"

# 11. Send transaction
DUMMY_TX="0xf86b0185098bca5a00825208940000000000000000000000000000000000000000880de0b6b3a76400008025a04f"
test_method_with_params "eth_sendRawTransaction" "[\"$DUMMY_TX\"]" "Send raw tx" "ETH"

# ===========================
# zkEVM Methods (28)
# ===========================
echo ""
echo "Testing zkEVM Specific Methods..."
echo "---------------------------------------------------------------------------------------------------------"

# 1. Simple query methods
test_method_with_params "zkevm_batchNumber" "[]" "Get batch number" "zkEVM"
test_method_with_params "zkevm_consolidatedBlockNumber" "[]" "Consolidated block" "zkEVM"
test_method_with_params "zkevm_getLatestGlobalExitRoot" "[]" "Latest GER" "zkEVM"
test_method_with_params "zkevm_getExitRootTable" "[]" "Exit root table" "zkEVM"
test_method_with_params "zkevm_getVersionHistory" "[]" "Version history" "zkEVM"
test_method_with_params "zkevm_getForks" "[]" "Get forks" "zkEVM"
test_method_with_params "zkevm_getLatestDataStreamBlock" "[]" "Latest DS block" "zkEVM"
test_method_with_params "zkevm_getBroadcastURI" "[]" "Broadcast URI" "zkEVM"
test_method_with_params "zkevm_getRollupAddress" "[]" "Rollup address" "zkEVM"
test_method_with_params "zkevm_getRollupManagerAddress" "[]" "Rollup mgr address" "zkEVM"
test_method_with_params "zkevm_getSequencerAddress" "[]" "Sequencer address" "zkEVM"
test_method_with_params "zkevm_getForkId" "[]" "Current fork ID" "zkEVM"

# 2. Block-related methods
test_method_with_params "zkevm_isBlockConsolidated" "[\"0x100\"]" "Block consolidated" "zkEVM"
test_method_with_params "zkevm_isBlockVirtualized" "[\"0x100\"]" "Block virtualized" "zkEVM"
test_method_with_params "zkevm_batchNumberByBlockNumber" "[\"0x100\"]" "Batch by block" "zkEVM"
test_method_with_params "zkevm_getFullBlockByNumber" "[\"0x100\"]" "Full block by num" "zkEVM"
test_method_with_params "zkevm_getFullBlockByHash" "[\"$BLOCK_HASH\"]" "Full block by hash" "zkEVM"
test_method_with_params "zkevm_getNativeBlockHashesInRange" "[\"0x100\", \"0x110\"]" "Block hash range" "zkEVM"

# 3. Batch-related methods
test_method_with_params "zkevm_getBatchByNumber" "[\"0xa\"]" "Get batch" "zkEVM"
test_method_with_params "zkevm_getBatchCountersByNumber" "[\"0xa\"]" "Batch counters" "zkEVM"
test_method_with_params "zkevm_getForkIdByBatchNumber" "[\"0xa\"]" "Fork by batch" "zkEVM"
test_method_with_params "zkevm_getBatchDataByNumbers" "[\"0xa\", \"0xb\"]" "Batch data range" "zkEVM"

# 4. Witness and proof methods
test_method_with_params "zkevm_getWitness" "[\"0x100\", true]" "Get witness" "zkEVM"
test_method_with_params "zkevm_getBlockRangeWitness" "[\"0x100\", \"0x110\"]" "Block range witness" "zkEVM"
test_method_with_params "zkevm_getBatchWitness" "[\"0xa\"]" "Batch witness" "zkEVM"
test_method_with_params "zkevm_getProverInput" "[\"0xa\", \"0x0000000000000000000000000000000000000000000000000000000000000000\", \"0x64\"]" "Prover input" "zkEVM"
test_method_with_params "zkevm_getProof" "[\"$ADDRESS\", [\"0x0\"], \"0x100\"]" "Get proof" "zkEVM"
test_method_with_params "zkevm_getProofByGER" "[\"0x0000000000000000000000000000000000000000000000000000000000000000\", \"0x100\"]" "Proof by GER" "zkEVM"

# 5. Exit root related
test_method_with_params "zkevm_getExitRootsByGER" "[\"0x0000000000000000000000000000000000000000000000000000000000000000\"]" "Exit roots by GER" "zkEVM"

# 6. L2 Block Info Tree
test_method_with_params "zkevm_getL2BlockInfoTree" "[\"0x100\", \"0x110\"]" "L2 block info tree" "zkEVM"

# 7. Estimate counters
test_method_with_params "zkevm_estimateCounters" "[{\"from\":\"0xf39fd6e51aad88f6f4ce6ab8827279cfffb92266\",\"to\":\"0x0000000000000000000000000000000000000000\",\"value\":\"0x0\",\"data\":\"0x\"}]" "Estimate counters" "zkEVM"

# 8. Fork related
test_method_with_params "zkevm_getForkById" "[1]" "Get fork by ID" "zkEVM"

# 9. Virtual and verified batch (may not be implemented)
test_method_with_params "zkevm_virtualBatchNumber" "[]" "Virtual batch num" "zkEVM"
test_method_with_params "zkevm_verifiedBatchNumber" "[]" "Verified batch num" "zkEVM"

echo "========================================================================================================="
echo ""
echo "Phase 3: Test Summary"
echo "========================================================================================================="

# Calculate averages
if [ $eth_success -gt 0 ]; then
    eth_avg_qps=$(echo "scale=2; $eth_total_qps / $eth_success" | bc)
else
    eth_avg_qps="0"
fi

if [ $zkevm_success -gt 0 ]; then
    zkevm_avg_qps=$(echo "scale=2; $zkevm_total_qps / $zkevm_success" | bc)
else
    zkevm_avg_qps="0"
fi

if [ $success_methods -gt 0 ]; then
    overall_avg_qps=$(echo "scale=2; $total_qps / $success_methods" | bc)
else
    overall_avg_qps="0"
fi

# Print summary table
echo ""
echo "Category Summary:"
echo "---------------------------------------------------------------------------------------------------------"
printf "%-15s | %8s | %8s | %8s | %12s | %12s\n" "Category" "Total" "Success" "Failed" "Total QPS" "Avg QPS"
echo "---------------------------------------------------------------------------------------------------------"
printf "%-15s | %8d | %8d | %8d | %12.2f | %12.2f\n" "ETH Standard" "$eth_methods" "$eth_success" "$((eth_methods - eth_success))" "$eth_total_qps" "$eth_avg_qps"
printf "%-15s | %8d | %8d | %8d | %12.2f | %12.2f\n" "zkEVM Specific" "$zkevm_methods" "$zkevm_success" "$((zkevm_methods - zkevm_success))" "$zkevm_total_qps" "$zkevm_avg_qps"
echo "---------------------------------------------------------------------------------------------------------"
printf "%-15s | %8d | %8d | %8d | %12.2f | %12.2f\n" "TOTAL" "$total_methods" "$success_methods" "$failed_methods" "$total_qps" "$overall_avg_qps"
echo "========================================================================================================="

# Sort and display top performers
echo ""
echo "Top 10 Performers (by QPS):"
echo "---------------------------------------------------------------------------------------------------------"
printf "%-5s | %-40s | %12s\n" "Type" "Method" "QPS"
echo "---------------------------------------------------------------------------------------------------------"
printf '%s\n' "${all_results[@]}" | sort -t'|' -k3 -rn | head -10 | while IFS='|' read -r type method qps avg max status; do
    printf "%-5s | %-40s | %12s\n" "$type" "$method" "$qps"
done

echo ""
echo "Bottom 10 Performers (by QPS, excluding failures):"
echo "---------------------------------------------------------------------------------------------------------"
printf "%-5s | %-40s | %12s\n" "Type" "Method" "QPS"
echo "---------------------------------------------------------------------------------------------------------"
printf '%s\n' "${all_results[@]}" | grep -v "|0|" | sort -t'|' -k3 -n | head -10 | while IFS='|' read -r type method qps avg max status; do
    printf "%-5s | %-40s | %12s\n" "$type" "$method" "$qps"
done

# Success rate
success_rate=$(echo "scale=2; $success_methods * 100 / $total_methods" | bc)

echo ""
echo "========================================================================================================="
echo "Overall Statistics:"
echo "---------------------------------------------------------------------------------------------------------"
echo "Test Duration: ~$(echo "$total_methods * 3" | bc) seconds"
echo "Success Rate: ${success_rate}%"
echo "Total Methods Tested: $total_methods (ETH: $eth_methods, zkEVM: $zkevm_methods)"
echo "Average QPS: $overall_avg_qps"
echo "========================================================================================================="
echo "Test completed at: $(date '+%Y-%m-%d %H:%M:%S')"
echo "========================================================================================================="
