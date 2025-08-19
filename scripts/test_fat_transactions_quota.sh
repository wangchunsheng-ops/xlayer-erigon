#!/bin/bash

# Fat Transaction Test Script
# This script tests the fat transaction detection, isolation, and quota allocation functionality

set -e

# Configuration
RPC_ENDPOINT="http://localhost:8123"
MAIN_PRIVATE_KEY="0x815405dddb0e2a99b12af775fd2929e526704e1d1aea6a0b4e74dc33e2f7fcd2"
NORMAL_PRIVATE_KEY="0x815405dddb0e2a99b12af775fd2929e526704e1d1aea6a0b4e74dc33e2f7fcd2"
FAT_PRIVATE_KEY="0xcc4c4c8374cdfdb6ddb20cdea1900e6059e1326187ccfff1de174ec706a619a7"
RECIPIENT_ADDRESS="0x742d35Cc6634C0532925a3b8D4C9db96C4b4d8b6"

# Gas limits
NORMAL_TX_GAS=21000
FAT_TX_GAS=2000000  # 2M gas (6.67% of 30M block gas limit)

# Number of transactions (balanced for quota testing)
NORMAL_TX_COUNT=50
FAT_TX_COUNT=50

echo "🚀 Starting Fat Transaction Test..."
echo "RPC Endpoint: $RPC_ENDPOINT"
echo "Private Key: $PRIVATE_KEY"
echo "Recipient: $RECIPIENT_ADDRESS"
echo ""

# Get sender addresses from private keys
NORMAL_SENDER_ADDRESS=$(cast wallet address --private-key $NORMAL_PRIVATE_KEY)
FAT_SENDER_ADDRESS=$(cast wallet address --private-key $FAT_PRIVATE_KEY)
echo "📋 Normal Sender Address: $NORMAL_SENDER_ADDRESS"
echo "📋 Fat Sender Address: $FAT_SENDER_ADDRESS"

# Fund fat transaction account if needed
echo ""
echo "💰 Funding fat transaction account..."
FAT_BALANCE=$(cast balance $FAT_SENDER_ADDRESS --rpc-url $RPC_ENDPOINT)
echo "📋 Fat account balance: $FAT_BALANCE wei"

if [ "$FAT_BALANCE" -lt 1000000000000000000 ]; then  # Less than 1 ETH
    echo "📤 Transferring funds to fat account..."
    cast send $FAT_SENDER_ADDRESS \
        --private-key $MAIN_PRIVATE_KEY \
        --value 1ether \
        --rpc-url $RPC_ENDPOINT \
        --legacy > /dev/null 2>&1
    echo "✅ Funds transferred to fat account"
else
    echo "✅ Fat account has sufficient funds"
fi

# Get current nonces
NORMAL_NONCE=$(cast nonce $NORMAL_SENDER_ADDRESS --rpc-url $RPC_ENDPOINT)
FAT_NONCE=$(cast nonce $FAT_SENDER_ADDRESS --rpc-url $RPC_ENDPOINT)
echo "📋 Normal Nonce: $NORMAL_NONCE"
echo "📋 Fat Nonce: $FAT_NONCE"

# Get starting block number
START_BLOCK=$(cast block-number --rpc-url $RPC_ENDPOINT)
echo "📋 Starting Block: $START_BLOCK"

# Function to send transaction without waiting
send_transaction_async() {
    local private_key=$1
    local nonce=$2
    local gas_limit=$3
    local description=$4
    
    # Send transaction in background
    cast send \
        $RECIPIENT_ADDRESS \
        --private-key $private_key \
        --value 0.001ether \
        --gas-limit $gas_limit \
        --nonce $nonce \
        --rpc-url $RPC_ENDPOINT \
        --legacy \
        --json > /dev/null 2>&1 &
    
    echo "📤 Sent $description (nonce: $nonce)"
}

# Send normal and fat transactions concurrently
echo "=== 📄🐘 Sending Normal and Fat Transactions Concurrently ==="

# Start normal transactions in background
for i in $(seq 1 $NORMAL_TX_COUNT); do
    send_transaction_async $NORMAL_PRIVATE_KEY $NORMAL_NONCE $NORMAL_TX_GAS "Normal TX $i" &
    NORMAL_NONCE=$((NORMAL_NONCE + 1))
done

# Start fat transactions in background
for i in $(seq 1 $FAT_TX_COUNT); do
    send_transaction_async $FAT_PRIVATE_KEY $FAT_NONCE $FAT_TX_GAS "Fat TX $i" &
    FAT_NONCE=$((FAT_NONCE + 1))
done

echo ""
echo "⏳ Waiting for all transactions to be sent..."
wait

echo ""
echo "📊 Checking transaction pool status..."
sleep 3

# Check txpool status
TXPOOL_STATUS=$(cast tx-pool status --rpc-url $RPC_ENDPOINT)
echo "TxPool Status: $TXPOOL_STATUS"

# Wait for blocks to be mined
echo ""
echo "⏳ Waiting for blocks to be mined (10 seconds)..."
sleep 10

# Get ending block number
END_BLOCK=$(cast block-number --rpc-url $RPC_ENDPOINT)
echo "📋 Ending Block: $END_BLOCK"

# Analyze blocks with quota focus
echo ""
echo "=== 📊 Analyzing Block Composition ==="
echo "Analyzing blocks from $START_BLOCK to $END_BLOCK"

total_normal_in_blocks=0
total_fat_in_blocks=0
blocks_with_txs=0

for block_num in $(seq $START_BLOCK $END_BLOCK); do
    # Get block transactions
    block_txs=$(cast block $block_num --rpc-url $RPC_ENDPOINT --json | jq -r '.transactions[]' 2>/dev/null || echo "")
    
    if [ -n "$block_txs" ]; then
        normal_count=0
        fat_count=0
        total_gas=0
        
        while IFS= read -r tx_hash; do
            if [ -n "$tx_hash" ]; then
                # Get transaction details
                tx_data=$(cast tx $tx_hash --rpc-url $RPC_ENDPOINT --json 2>/dev/null || echo "")
                if [ -n "$tx_data" ]; then
                    gas_limit=$(echo $tx_data | jq -r '.gas')
                    gas_limit_decimal=$(printf "%d" $gas_limit)
                    
                    # Determine if it's a fat transaction
                    if [ $gas_limit_decimal -ge $FAT_TX_GAS ]; then
                        fat_count=$((fat_count + 1))
                    else
                        normal_count=$((normal_count + 1))
                    fi
                    
                    total_gas=$((total_gas + gas_limit_decimal))
                fi
            fi
        done <<< "$block_txs"
        
        if [ $((normal_count + fat_count)) -gt 0 ]; then
            blocks_with_txs=$((blocks_with_txs + 1))
            total_normal_in_blocks=$((total_normal_in_blocks + normal_count))
            total_fat_in_blocks=$((total_fat_in_blocks + fat_count))
            
            echo "📦 Block $block_num: $normal_count normal + $fat_count fat = $((normal_count + fat_count)) total (gas: $total_gas)"
            
            # Calculate ratios for this block
            if [ $((normal_count + fat_count)) -gt 0 ]; then
                normal_ratio=$(echo "scale=1; $normal_count * 100 / ($normal_count + $fat_count)" | bc -l 2>/dev/null || echo "N/A")
                fat_ratio=$(echo "scale=1; $fat_count * 100 / ($normal_count + $fat_count)" | bc -l 2>/dev/null || echo "N/A")
                echo "   📊 Block ratios: Normal $normal_ratio%, Fat $fat_ratio%"
            fi
        fi
    fi
done

echo ""
echo "=== 📈 Overall Quota Analysis ==="
echo "📊 Total transactions in blocks: $((total_normal_in_blocks + total_fat_in_blocks))"
echo "📊 Normal transactions: $total_normal_in_blocks"
echo "📊 Fat transactions: $total_fat_in_blocks"
echo "📊 Blocks with transactions: $blocks_with_txs"

if [ $((total_normal_in_blocks + total_fat_in_blocks)) -gt 0 ]; then
    overall_normal_ratio=$(echo "scale=1; $total_normal_in_blocks * 100 / ($total_normal_in_blocks + $total_fat_in_blocks)" | bc -l 2>/dev/null || echo "N/A")
    overall_fat_ratio=$(echo "scale=1; $total_fat_in_blocks * 100 / ($total_normal_in_blocks + $total_fat_in_blocks)" | bc -l 2>/dev/null || echo "N/A")
    echo "📊 Overall ratios: Normal $overall_normal_ratio%, Fat $overall_fat_ratio%"
fi

echo ""
echo "🎯 Expected Quota: Normal 80%, Fat 20%"
echo "📊 Actual Quota: Normal $overall_normal_ratio%, Fat $overall_fat_ratio%"
echo ""
echo "📈 Test Summary:"
echo "  - Total transactions sent: $((NORMAL_TX_COUNT + FAT_TX_COUNT))"
echo "  - Normal transactions: $NORMAL_TX_COUNT"
echo "  - Fat transactions: $FAT_TX_COUNT"
echo "  - Blocks analyzed: $((END_BLOCK - START_BLOCK + 1))"

echo ""
echo "🎉 Fat Transaction Test Complete!"
