dofile("common.lua")
methodName = "eth_getBalance"
wrk.method = "POST"
wrk.headers["Content-Type"] = "application/json"

threads = {}

function readAll(file)
    local f = assert(io.open(file, "rb"))
    local content = f:read("*all")
    f:close()
    return content
end

function Split(inputstr, sep)
    if sep == nil then
        sep = "%s"
    end
    local t={}
    for str in string.gmatch(inputstr, "([^"..sep.."]+)") do
        table.insert(t, str)
    end
    return t
end

function isTableEmpty(t)
    return t == nil or next(t) == nil
end

path = 'accounts.txt'
values = {}
counter = 1
content = readAll(path)
if isTableEmpty(values) then
    -- Load once if empty
    values = Split(content,"\n")
end

setup = function(thread)
    print("len content:",#content)
    print("len values:",#values)

    thread:set("t_id",counter)
    print("counter:"..counter)
    counter = counter + 1
    thread:set("counter_invalid", 0)
    thread:set("counter_valid", 0)
    thread:set("counter_failed", 0)
    thread:set("length", #values)
    table.insert(threads,thread)
    math.randomseed(os.time()+counter)
end

request = function()
    local t_id = wrk.thread:get("t_id")
    local len = wrk.thread:get("length")
    local index = math.random(1,len) -- Supports 16 threads and 20,000 addresses
    local value = values[index]

    local body = string.format('{"jsonrpc":"2.0","method":"eth_getBalance","params":["%s","latest"],"id":1}', value)
    -- print(body)
    headers = {}
    headers["Content-Type"] = "application/json"
    return wrk.format("POST",nil,headers,body)
end

response = handle_response

done = function(summary, latency, requests)
    print_summary(summary, latency, requests, threads, methodName)
end
