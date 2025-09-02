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
echo "                          zkEVM RPC Stress Test Report with Parameters"
echo "========================================================================================================="
echo "Test Node: $BASE_URL"
echo "Test Time: $(date '+%Y-%m-%d %H:%M:%S')"
echo "Test Config: 2 threads, 5 connections, 5 seconds duration"
echo "---------------------------------------------------------------------------------------------------------"
printf "%-35s | %12s | %12s | %12s | %-15s\n" "Method" "QPS" "Avg(ms)" "Max(ms)" "Status"
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
    cat > temp_test.lua << EOF
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
    output=$(wrk $WRK_PARAMS -s temp_test.lua "$BASE_URL" 2>&1)
    
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
    
    # Output results
    printf "%-35s | %12s | %12s | %12s | %-15s\n" "$method" "$qps" "$avg_ms" "$max_ms" "$status"
    
    # Clean up temporary file
    rm -f temp_test.lua
    
    sleep 0.5
}

# Test methods that require parameters

# 1. Block-related methods - using block number 256 (0x100)
test_method_with_params "zkevm_isBlockConsolidated" '["0x100"]' "区块256是否已巩固"
test_method_with_params "zkevm_isBlockVirtualized" '["0x100"]' "区块256是否已虚拟化"
test_method_with_params "zkevm_batchNumberByBlockNumber" '["0x100"]' "区块256的批次号"
test_method_with_params "zkevm_getFullBlockByNumber" '["0x100"]' "获取区块256完整信息"

# 2. Using block hash
test_method_with_params "zkevm_getFullBlockByHash" '["0x9cfcad6bfecb7a218c25483dfcc18f41073f5818649686f503682c02cff6e3c0"]' "通过哈希获取区块"

# 3. Batch-related methods - using batch number 10
test_method_with_params "zkevm_getBatchByNumber" '["0xa"]' "获取批次10信息"
test_method_with_params "zkevm_getBatchCountersByNumber" '["0xa"]' "获取批次10计数器"
test_method_with_params "zkevm_getForkIdByBatchNumber" '["0xa"]' "获取批次10的fork ID"

# 4. Witness-related methods
test_method_with_params "zkevm_getWitness" '["0x100", true]' "获取区块256的witness"
test_method_with_params "zkevm_getBlockRangeWitness" '["0x100", "0x110"]' "获取区块范围witness"
test_method_with_params "zkevm_getBatchWitness" '["0xa"]' "获取批次10的witness"

# 5. Proof-related
test_method_with_params "zkevm_getProverInput" '["0xa", "0x0000000000000000000000000000000000000000000000000000000000000000", "0x64"]' "获取证明输入"

# 6. Exit Root related
test_method_with_params "zkevm_getExitRootsByGER" '["0x0000000000000000000000000000000000000000000000000000000000000000"]' "通过GER获取exit roots"

# 7. L2 Block Info Tree
test_method_with_params "zkevm_getL2BlockInfoTree" '["0x100", "0x110"]' "获取L2区块信息树"

# 8. Estimate counters
test_method_with_params "zkevm_estimateCounters" '[{"from":"0xf39fd6e51aad88f6f4ce6ab8827279cfffb92266","to":"0x0000000000000000000000000000000000000000","value":"0x0","data":"0x"}]' "估算交易计数器"

# 9. Fork related
test_method_with_params "zkevm_getForkById" '[1]' "获取fork ID 1的信息"

# 10. Methods without parameters (comparison test)
test_method_with_params "zkevm_consolidatedBlockNumber" '[]' "获取已巩固区块号"
test_method_with_params "zkevm_virtualBatchNumber" '[]' "获取虚拟批次号"
test_method_with_params "zkevm_verifiedBatchNumber" '[]' "获取已验证批次号"

# Calculate average QPS
if [ $success_methods -gt 0 ]; then
    avg_qps=$(echo "scale=2; $total_qps / $success_methods" | bc)
else
    avg_qps=0
fi

echo "========================================================================================================="
echo ""
echo "📊 Test Summary"
echo "-----------------------------------------"
echo "  Total methods tested: $total_methods"
echo "  ✅ Success: $success_methods"
echo "  ❌ Failed: $failed_methods"
echo "  Average QPS: $avg_qps"
echo "  Test completed at: $(date '+%Y-%m-%d %H:%M:%S')"
echo ""
echo "Note: Tests use reasonable parameter values like block 256, batch 10, etc."
echo "========================================================================================================="
