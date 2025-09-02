dofile("common.lua")
methodName = "eth_createAccessList"
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
    local from = "0x100d1b939151598373E4BbAAc7435eE9C4dec7A1"
    local to = "0x7a250d5630B4cF539739dF2C5dAcb4c659F2488D"
    local gas = "0x4c4b40"
    local gasPrice = "0xba43b7400"
    local data = "0x18cbafe500000000000000000000000000000000000000000000000000000000000186a0000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000a00000000000000000000000009aFc51c7867f369371F238674ee4459556c5D2b50000000000000000000000000000000000000000000000000000000061d133750000000000000000000000000000000000000000000000000000000000000002000000000000000000000000a0b86991c6218b36c1d19d4a2e9eb0ce3606eb48000000000000000000000000c02aaa39b223fe8d0a0e5c4f27ead9083c756cc2"
    local body = string.format('{"jsonrpc":"2.0","method":"%s","params":[{"from":"%s","to":"%s","gasPrice":"%s","gas":"%s", "data":"%s"},"latest", true],"id":1}', methodName, from, to,gasPrice, gas, data)
    -- print(body)
    headers = {}
    headers["Content-Type"] = "application/json"
    return wrk.format("POST",nil,headers,body)
end

response = handle_response

done = function(summary, latency, requests)
    print_summary(summary, latency, requests, threads, methodName)
end
