dofile("common.lua")
methodName = "eth_estimateGas"
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
    local value = "0xde0b6b3a7640000"
    local from = "0x5075ff68a0efb54db13423ad924bd680327d305e"
    local to = "0xcd5f731b3b77737743f80a3d4b0722b710f549cb"
    local data = "0x3a4b66f1"
    local body = string.format('{"jsonrpc":"2.0","method":"%s","params":[{"value":"%s","from":"%s","to":"%s","data":"%s"},"latest"],"id":1}', methodName, value, from, to, data)
    -- print(body)
    headers = {}
    headers["Content-Type"] = "application/json"
    return wrk.format("POST",nil,headers,body)
end

response = handle_response

done = function(summary, latency, requests)
    print_summary(summary, latency, requests, threads, methodName)
end
