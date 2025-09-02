#!/bin/sh

BASE_URL="http://localhost:8124"  # xlayer-rpc local endpoint

methods=(
    "consolidatedBlockNumber"
    "isBlockConsolidated"
    "isBlockVirtualized"
    "batchNumberByBlockNumber"
    "batchNumber"
    "virtualBatchNumber"
    "verifiedBatchNumber"
    "getBatchByNumber"
    "getFullBlockByNumber"
    "getFullBlockByHash"
    "getWitness"
    "getBlockRangeWitness"
    "getBatchWitness"
    "getProverInput"
    "getLatestGlobalExitRoot"
    "getExitRootsByGER"
    "getL2BlockInfoTree"
    "estimateCounters"
    "getBatchCountersByNumber"
    "getExitRootTable"
    "getVersionHistory"
    "getForkId"
    "getForkById"
    "getForkIdByBatchNumber"
    "getForks"
    "getRollupAddress"
    "getRollupManagerAddress"
    "getLatestDataStreamBlock"
)


if command -v wrk2 >/dev/null 2>&1; then
    # Lightweight test parameters: 2 threads, 5 connections, 5 seconds test duration
    WRK_COMMAND="wrk2 -t 2 -c 5 -d 5s -T 10s -R 50 -L -s"
    echo "Using wrk2 with light load for local testing"
elif command -v wrk >/dev/null 2>&1; then
    # Lightweight test parameters: 2 threads, 5 connections, 5 seconds test duration
    WRK_COMMAND="wrk -t 2 -c 5 -d 5s -T 10s -s"
    echo "Using original wrk with light load for local testing"
else
    echo "Error: Neither wrk nor wrk2 found in PATH" >&2
    exit 1
fi


for method in "${methods[@]}"
do
    method_name="$method"
    $WRK_COMMAND "$method_name.lua" "$BASE_URL"
    printf "\n"
done