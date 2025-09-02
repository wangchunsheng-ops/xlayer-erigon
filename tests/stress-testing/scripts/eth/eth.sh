#! /bin/sh

BASE_URL="https://xlayerrpc.okx.com"

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
    "blockNumber"
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

if command -v wrk2 >/dev/null 2>&1; then
    WRK_COMMAND="wrk2 -t 2 -c 10 -d 10s -T 10s -R 100 -L -s"
    echo "Using wrk2"
elif command -v wrk >/dev/null 2>&1; then
#    -t 16 -c 5000 -d 60s -T 30s -s
    WRK_COMMAND="wrk -t 2 -c 10 -d 10s -T 10s -s"
    echo "Using original wrk"
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