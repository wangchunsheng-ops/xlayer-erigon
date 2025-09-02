dofile("common.lua")
methodName = "eth_getCode"
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
    local address = "0x881fB2f98c13d521009464e7D1CBf16E1b394e8E"
    local blockNumber = "finalized"
    local body = string.format('{"jsonrpc":"2.0","method":"%s","params":["%s","%s"],"id":1}', methodName, address, blockNumber)
    headers = {}
    headers["Content-Type"] = "application/json"
    return wrk.format("POST", nil, headers, body)
end

response = handle_response

done = function(summary, latency, requests)
    print_summary(summary, latency, requests, threads, methodName)
end