dofile("common.lua")
methodName = "zkevm_getFullBlockByHash"
wrk.method = "POST"
wrk.headers["Content-Type"] = "application/json"

threads = {}
counter = 1

setup = function(thread)
    thread:set("t_id", counter)
    counter = counter + 1
    thread:set("counter_invalid", 0)
    thread:set("counter_valid", 0)
    thread:set("counter_failed", 0)
    table.insert(threads, thread)
    math.randomseed(os.time() + counter)
end

request = function()
    local block_hash= "0x5bdeaa85bc72fb7d01e8ffdbca5c15781e3e24225308e4e7aededb9b9f6f2478"
    local body = string.format('{"jsonrpc":"2.0","method":"%s","params":[],"id":1}', methodName, block_hash)
    headers = {}
    headers["Content-Type"] = "application/json"
    return wrk.format("POST", nil, headers, body)
end

response = handle_response

done = function(summary, latency, requests)
    print_summary(summary, latency, requests, threads, methodName)
end