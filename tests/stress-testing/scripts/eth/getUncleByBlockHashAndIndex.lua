dofile("common.lua")
methodName = "eth_getUncleByBlockHashAndIndex"
wrk.method = "POST"
wrk.headers["Content-Type"] = "application/json"

threads = {}
counter = 1

setup = function(thread)
    thread:set("t_id",counter)
    counter = counter + 1
    thread:set("counter_invalid", 0)
    thread:set("counter_valid", 0)
    thread:set("counter_failed", 0)
    table.insert(threads,thread)
    math.randomseed(os.time()+counter)
end

request = function()
    local hash = "0x639442459fb37cd331062d0a2c055f0a827561d3b2f0c7ac0d06e5ce205229f1"
    local index = 0
    local body = string.format('{"jsonrpc":"2.0","method":"%s","params":["%s","0x%X"],"id":1}', methodName, hash, index)
    --print(body)
    headers = {}
    headers["Content-Type"] = "application/json"
    return wrk.format("POST",nil,headers,body)
end

response = handle_response

done = function(summary, latency, requests)
    print_summary(summary, latency, requests, threads, methodName)
end