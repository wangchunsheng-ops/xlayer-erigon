dofile("common.lua")
methodName = "zkevm_getExitRootsByGER"
wrk.method = "POST"
wrk.headers["Content-Type"] = "application/json"

threads = {}
counter = 1

setup = function(thread)
    initialize(thread, threads, counter)
end

request = function()
    local exit_root = "0x527ea5f2db0b0212b337b1fdf52e1f2a7bd9e5db97d6dafe8fef9b2967ac8319"
    local body = string.format('{"jsonrpc":"2.0","method":"%s","params":[],"id":1}', methodName)
    headers = {}
    headers["Content-Type"] = "application/json"
    return wrk.format("POST", nil, headers, body)
end

response = handle_response

done = function(summary, latency, requests)
    print_summary(summary, latency, requests, threads, methodName)
end