dofile("common.lua")
methodName = "eth_getStorageAt"
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
    local address = "0xce3843f1ea54833a2c95feac4959c0644a595cfe"
    local index = "0x100"
    local blockNumber = "0xAAC8DE"
    local body = string.format('{"jsonrpc":"2.0","method":"%s","params":["%s","%s","%s"],"id":1}', methodName, address, index, blockNumber)
    --print(body)
    headers = {}
    headers["Content-Type"] = "application/json"
    return wrk.format("POST",nil,headers,body)
end

response = handle_response

done = function(summary, latency, requests)
    print_summary(summary, latency, requests, threads, methodName)
end