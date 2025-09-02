dofile("common.lua")
methodName = "eth_getRawTransactionByBlockNumberAndIndex"
wrk.method = "POST"
wrk.headers["Content-Type"] = "application/json"

threads = {}
counter = 1

setup = function(thread)
    initialize(thread, threads, counter)
end

request = function()
    local block = math.random(1, 11276923) -- Supports 16 threads, 20,000 address parameters
    local body = string.format('{"jsonrpc":"2.0","method":"%s","params":["0x%X", 0],"id":1}', methodName, block)
    headers = {}
    headers["Content-Type"] = "application/json"
    return wrk.format("POST", nil, headers, body)
end

response = handle_response

done = function(summary, latency, requests)
    print_summary(summary, latency, requests, threads, methodName)
end
