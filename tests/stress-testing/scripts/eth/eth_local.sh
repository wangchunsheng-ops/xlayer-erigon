#!/bin/sh

BASE_URL="http://localhost:8124"  # Local RPC node

# ETH standard RPC methods list
methods=(
    "blockNumber"
    "getBlockByNumber"
    "getBlockByHash"
    "getBlockTransactionCountByNumber"
    "getBlockTransactionCountByHash"
    "getTransactionByHash"
    "getTransactionByBlockHashAndIndex"
    "getTransactionByBlockNumberAndIndex"
    "getRawTransactionByBlockNumberAndIndex"
    "getRawTransactionByBlockHashAndIndex"
    "getRawTransactionByHash"
    "getTransactionReceipt"
    "getLogs"
    "getBlockReceipts"
    "getUncleByBlockNumberAndIndex"
    "getUncleByBlockHashAndIndex"
    "getUncleCountByBlockNumber"
    "getUncleCountByBlockHash"
    "newPendingTransactionFilter"
    "newBlockFilter"
    "newFilter"
    "uninstallFilter"
    "getFilterChanges"
    "getFilterLogs"
    "accounts"
    "getBalance"
    "getTransactionCount"
    "getStorageAt"
    "getCode"
    "syncing"
    "chainId"
    "protocolVersion"
    "gasPrice"
    "estimateGas"
    "call"
    "sendRawTransaction"
    "getProof"
    "createAccessList"
)

# Check if wrk is installed
if command -v wrk2 >/dev/null 2>&1; then
    # Lightweight test parameters
    WRK_COMMAND="wrk2 -t 2 -c 5 -d 5s -T 10s -R 50 -L -s"
    echo "Using wrk2 with light load for local testing"
elif command -v wrk >/dev/null 2>&1; then
    # Lightweight test parameters
    WRK_COMMAND="wrk -t 2 -c 5 -d 5s -T 10s -s"
    echo "Using original wrk with light load for local testing"
else
    echo "Error: Neither wrk nor wrk2 found in PATH" >&2
    exit 1
fi

echo ""
echo "========================================================================================================="
echo "                              ETH RPC Local Node Stress Test"
echo "========================================================================================================="
echo "Test Node: $BASE_URL"
echo "Test Time: $(date '+%Y-%m-%d %H:%M:%S')"
echo "Test Config: 2 threads, 5 connections, 5 seconds duration"
echo "---------------------------------------------------------------------------------------------------------"
printf "%-40s | %12s | %12s | %12s | %-10s\n" "Method" "QPS" "Avg(ms)" "Max(ms)" "Status"
echo "========================================================================================================="

# Statistics variables
total_qps=0
total_methods=0
success_methods=0
failed_methods=0
declare -a results

# Test each method
for method in "${methods[@]}"
do
    method_name="$method"
    lua_file="${method_name}.lua"
    
    # Check if lua file exists
    if [ ! -f "$lua_file" ]; then
        printf "%-40s | %12s | %12s | %12s | %-10s\n" "eth_$method" "N/A" "N/A" "N/A" "❌ Script missing"
        ((failed_methods++))
        ((total_methods++))
        continue
    fi
    
    # Run test
    output=$($WRK_COMMAND "$lua_file" "$BASE_URL" 2>&1)
    
    # Extract QPS
    qps=$(echo "$output" | grep "Requests/sec:" | awk '{printf "%.2f", $2}')
    
    # Extract average latency
    avg_latency=$(echo "$output" | grep "Latency" | awk '{print $2}' | head -1)
    
    # Extract max latency
    max_latency=$(echo "$output" | grep "Latency" | awk '{print $4}' | head -1)
    
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
        # Store results for later sorting
        results+=("eth_$method|$qps|$avg_ms|$max_ms")
    fi
    
    ((total_methods++))
    
    # Output results
    printf "%-40s | %12s | %12s | %12s | %-10s\n" "eth_$method" "$qps" "$avg_ms" "$max_ms" "$status"
    
    # Test interval
    sleep 0.5
done

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

# Show performance TOP 10
if [ ${#results[@]} -gt 0 ]; then
    echo "🏆 Performance TOP 10 (Highest QPS):"
    echo "-----------------------------------------"
    for result in "${results[@]}"; do
        echo "$result"
    done | sort -t'|' -k2 -rn | head -10 | while IFS='|' read -r method qps avg max; do
        printf "  %-40s QPS: %s\n" "$method" "$qps"
    done
    echo ""
fi

echo "========================================================================================================="
