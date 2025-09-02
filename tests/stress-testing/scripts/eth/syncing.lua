dofile("common.lua")
methodName = "eth_syncing"
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
    local body = string.format('{"jsonrpc":"2.0","method":"%s","params":[],"id":1}', methodName)
    -- print(body)
    headers = {}
    headers["Content-Type"] = "application/json"
    return wrk.format("POST",nil,headers,body)
end

response = handle_response

done = function(summary, latency, requests)
    print_summary(summary, latency, requests, threads, methodName)
end
