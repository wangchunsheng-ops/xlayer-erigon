dofile("common.lua")
methodName = "eth_getTransactionReceipt"
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
    local hash = "0x08a943c4580651b7dc5ef8ff71303d03bab9b57875873c405100f7184db14607"
    local body = string.format('{"jsonrpc":"2.0","method":"%s","params":["%s"],"id":1}', methodName, hash)
    --     print(body)
    headers = {}
    headers["Content-Type"] = "application/json"
    return wrk.format("POST",nil,headers,body)
end

response = handle_response

done = function(summary, latency, requests)
    print_summary(summary, latency, requests, threads, methodName)
end