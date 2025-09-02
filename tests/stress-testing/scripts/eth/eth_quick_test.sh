#!/bin/sh

BASE_URL="http://localhost:8124"  # Local RPC node

# Test core ETH methods
methods=(
    "blockNumber"
    "chainId"
    "gasPrice"
    "syncing"
    "protocolVersion"
    "accounts"
)

# Use wrk for testing
if ! command -v wrk >/dev/null 2>&1; then
    echo "Error: wrk not found in PATH" >&2
    exit 1
fi

WRK_COMMAND="wrk -t 1 -c 2 -d 3s -T 10s -s"

echo ""
echo "========================================================================================================="
echo "                              ETH RPC Quick Test"
echo "========================================================================================================="
echo "Test Node: $BASE_URL"
echo "Test Time: $(date '+%Y-%m-%d %H:%M:%S')"
echo "---------------------------------------------------------------------------------------------------------"
printf "%-40s | %12s | %12s | %12s | %-10s\n" "Method" "QPS" "Avg(ms)" "Max(ms)" "Status"
echo "========================================================================================================="

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

# Test each method
for method in "${methods[@]}"
do
    lua_file="${method}.lua"
    
    if [ ! -f "$lua_file" ]; then
        printf "%-40s | %12s | %12s | %12s | %-10s\n" "eth_$method" "N/A" "N/A" "N/A" "❌ Script missing"
        continue
    fi
    
    # Run test
    output=$($WRK_COMMAND "$lua_file" "$BASE_URL" 2>&1)
    
    # Extract metrics
    qps=$(echo "$output" | grep "Requests/sec:" | awk '{printf "%.2f", $2}')
    avg_latency=$(echo "$output" | grep "Latency" | awk '{print $2}' | head -1)
    max_latency=$(echo "$output" | grep "Latency" | awk '{print $4}' | head -1)
    
    avg_ms=$(convert_to_ms "$avg_latency")
    max_ms=$(convert_to_ms "$max_latency")
    
    # Check status
    if [ -z "$qps" ] || [ "$qps" == "0" ]; then
        status="❌ Failed"
        qps="0"
    else
        status="✅ Success"
    fi
    
    printf "%-40s | %12s | %12s | %12s | %-10s\n" "eth_$method" "$qps" "$avg_ms" "$max_ms" "$status"
    
    sleep 0.5
done

echo "========================================================================================================="
echo "Quick test completed at: $(date '+%Y-%m-%d %H:%M:%S')"
echo "========================================================================================================="
