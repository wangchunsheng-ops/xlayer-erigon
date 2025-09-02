dofile("common.lua")
methodName = "eth_getTransactionByHash"
wrk.method = "POST"
wrk.headers["Content-Type"] = "application/json"

threads = {}
counter = 1

setup = function(thread)
    initialize(thread, threads, counter)
end

request = function()
    local hash = "0x7cbba4ec91f4c66d593db48f1ce879ef62bd45011ea84d33ae0f5e1f899b0c88"
    local fullTx = (math.random(0, 1) == 1)
    local body = string.format('{"jsonrpc":"2.0","method":"%s","params":["%s",%s],"id":1}', methodName, hash, tostring(fullTx))
--     print(body)
    headers = {}
    headers["Content-Type"] = "application/json"
    return wrk.format("POST",nil,headers,body)
end

response = handle_response

done = function(summary, latency, requests)
    print_summary(summary, latency, requests, threads, methodName)
end