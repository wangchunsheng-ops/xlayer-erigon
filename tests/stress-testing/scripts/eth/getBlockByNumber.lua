dofile("common.lua")
methodName = "eth_getBlockByNumber"
wrk.method = "POST"
wrk.headers["Content-Type"] = "application/json"

threads = {}
counter = 1

setup = function(thread)
    initialize(thread, threads, counter)
end

request = function()
    local block = math.random(1, 11276923) -- Supports 16 threads, 20,000 address parameters
    local fullTx = (math.random(0, 1) == 1)
    local body = string.format('{"jsonrpc":"2.0","method":"%s","params":["0x%X",%s],"id":1}', methodName, block, tostring(fullTx))
    headers = {}
    headers["Content-Type"] = "application/json"
    return wrk.format("POST", nil, headers, body)
end

response = handle_response

done = function(summary, latency, requests)
    print_summary(summary, latency, requests, threads, methodName)
end

