dofile("common.lua")
methodName = "eth_getTransactionByHash"
wrk.method = "POST"
wrk.headers["Content-Type"] = "application/json"

threads = {}
counter = 1

-- Real transaction hash from block #1 in the test environment
-- This should be updated based on your actual test node
local REAL_TX_HASH = "0x5885f4e6d05ba846030d137f00d43994b5b3062cb537614c337cda5192d3032b"

-- Fallback hash (from original script, may not exist in your node)
local FALLBACK_HASH = "0x7cbba4ec91f4c66d593db48f1ce879ef62bd45011ea84d33ae0f5e1f899b0c88"

setup = function(thread)
    initialize(thread, threads, counter)
end

request = function()
    -- Use real transaction hash if available, otherwise use fallback
    local hash = REAL_TX_HASH
    local fullTx = (math.random(0, 1) == 1)
    local body = string.format('{"jsonrpc":"2.0","method":"%s","params":["%s",%s],"id":1}', 
                              methodName, hash, tostring(fullTx))
    headers = {}
    headers["Content-Type"] = "application/json"
    return wrk.format("POST", nil, headers, body)
end

response = handle_response

done = function(summary, latency, requests)
    print_summary(summary, latency, requests, threads, methodName)
end
