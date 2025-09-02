dofile("common.lua")
methodName = "eth_getRawTransactionByBlockHashAndIndex"
wrk.method = "POST"
wrk.headers["Content-Type"] = "application/json"

threads = {}
counter = 1

setup = function(thread)
    initialize(thread, threads, counter)
end

request = function()
    local hash = "0x9fb8dfe1f6dd5d5e72c92237ee6d99af8814cddf31b786dfa6d674aabe965957"
    local body = string.format('{"jsonrpc":"2.0","method":"%s","params":["%s",0],"id":1}', methodName, hash)
    -- print(body)
    headers = {}
    headers["Content-Type"] = "application/json"
    return wrk.format("POST",nil,headers,body)
end

response = handle_response

done = function(summary, latency, requests)
    print_summary(summary, latency, requests, threads, methodName)
end
