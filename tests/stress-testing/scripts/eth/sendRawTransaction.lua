dofile("common.lua")
methodName = "eth_sendRawTransaction"
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
    local tx_payload= "0xf866808504a817c80082520894112a9a318865a2c4020364d33521941f88d52a2c80808201aba0a9341ccd9d6b0f0a133f557494ec93d7920e24c65574cdcc40034e172f0b599aa053536f29611891752ba8e4209828829ccc4331daff53fb4f84af5584b50550d3"
    local body = string.format('{"jsonrpc":"2.0","method":"%s","params":["%s"],"id":1}', methodName, tx_payload)
    -- print(body)
    headers = {}
    headers["Content-Type"] = "application/json"
    return wrk.format("POST",nil,headers,body)
end

response = handle_response

done = function(summary, latency, requests)
    print_summary(summary, latency, requests, threads, methodName)
end
