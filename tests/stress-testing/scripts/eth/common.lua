function print_summary(summary, latency, requests, threads,  methodName)
    local x4 = 0
    failTotal = 0
    sucTotal = 0
    if (threads ~= nil) then
        for index, thread in pairs(threads) do
            local counter_invalid = thread:get("counter_invalid")
            local counter_valid = thread:get("counter_valid")
            local counter_failed = thread:get("counter_failed")
            print(string.format("thread%d counter: %d", index, counter_failed))
            failTotal = failTotal + counter_invalid
            sucTotal = sucTotal + counter_valid
            x4 = x4 + counter_failed
        end
    end

    local durations = summary.duration / 1000000    -- Execution time in seconds
    local errors = summary.errors.status            -- HTTP status codes not starting with 200 or 300
    local requests = summary.requests               -- Total number of requests
    local valid = requests - x4                     -- Valid requests = total - failed
    local connect = summary.errors.connect
    local read1 = summary.errors.read
    local write1 = summary.errors.write
    local timeout = summary.errors.timeout
    local errorRate = (x4/requests)*100
    errorRate = string.format("%.1f",errorRate)

    io.write("+++++++++++++++++++++++++++++++++++++\n")
    io.write(" "..string.format("%s",methodName).."\n")
    io.write(" Test duration: "..string.format("%.2f",durations).."s".."\n")
    io.write(" Avg response time: "..string.format("%.2f",latency.mean / 1000).."ms".."\n")
    io.write(" Min response time: "..(latency.min / 1000).."ms".."\n")
    io.write(" Max response time: "..(latency.max / 1000).."ms".."\n")
    io.write(" Total requests: "..summary.requests.."\n")
    io.write(" Failed requests: "..x4.."\n")
    io.write(" Valid requests: "..valid.."\n")
    io.write(" Error rate: "..errorRate.."%\n")
    io.write(" Queries per second: "..string.format("%.2f",valid / durations).."\n")
    io.write(" Response assert failed: "..failTotal.."\n")
    io.write(" Response assert success: "..sucTotal.."\n")
    io.write("+++++++++++++++++++++++++++++++++++++\n")
end

function handle_response(status, headers, body)
    local counter_invalid = wrk.thread:get("counter_invalid")
    local counter_valid = wrk.thread:get("counter_valid")
    local counter_failed = wrk.thread:get("counter_failed")
    if string.find(body,'"error":') then
        counter_invalid = counter_invalid + 1
        --         print("Error response")
        --         print("Status: " .. status)
        --         print("Body: " .. body)
        -- print(1,body,"  ", "SET ",counter)
    elseif string.find(body,'"result":') then
        counter_valid = counter_valid + 1
        -- print(2,body)
    elseif not string.find(body,'"jsonrpc":') then
        counter_failed = counter_failed + 1
        --         print("RPC call failed")
        --         print("Status: " .. status)
        --         print("Body: " .. body)
    end
    wrk.thread:set("counter_invalid", counter_invalid)
    wrk.thread:set("counter_valid", counter_valid)
    wrk.thread:set("counter_failed", counter_failed)
end


function initialize(thread, threads, counter)
    thread:set("t_id", counter)
    counter = counter + 1
    thread:set("counter_invalid", 0)
    thread:set("counter_valid", 0)
    thread:set("counter_failed", 0)
    table.insert(threads, thread)
    math.randomseed(os.time() + counter)
end