-- KEYS[1] 全局桶、KEYS[2] 用户桶；每个 Hash 保存 tokens 与 updated_ms。
-- ARGV: 全局 rate/burst/满桶恢复毫秒，用户 rate/burst/满桶恢复毫秒，最大可返回毫秒。
local clock = redis.call('TIME')
local now = tonumber(clock[1]) * 1000 + math.floor(tonumber(clock[2]) / 1000)
local maxMillis = tonumber(ARGV[7])

local function readBucket(key, rate, burst, fullMillis)
    local kind = redis.call('TYPE', key).ok
    if kind == 'none' then
        return {tokens=burst, updated=now, rate=rate, burst=burst, ttl=fullMillis}
    end
    if kind ~= 'hash' then return nil end
    local state = redis.call('HMGET', key, 'tokens', 'updated_ms')
    local tokens, updated = tonumber(state[1]), tonumber(state[2])
    if not tokens or not updated or tokens ~= tokens or updated ~= updated or
       tokens < 0 or tokens == math.huge or updated < 0 or updated > 9007199254740991 or
       updated ~= math.floor(updated) then
        return nil
    end
    -- Redis 时钟回拨时不重复获得补充额度，也不把旧时间戳向后移动。
    local elapsed = math.max(0, now - updated)
    tokens = math.min(burst, tokens + elapsed * rate / 1000)
    updated = math.max(now, updated)
    local ttl = fullMillis + updated - now
    if ttl > maxMillis then return nil end
    return {tokens=tokens, updated=updated, rate=rate, burst=burst, ttl=ttl}
end

-- 两个桶全部读取并校验完毕后才写入，错误类型不能造成前一个桶部分扣减。
local global = readBucket(KEYS[1], tonumber(ARGV[1]), tonumber(ARGV[2]), tonumber(ARGV[3]))
local user = readBucket(KEYS[2], tonumber(ARGV[4]), tonumber(ARGV[5]), tonumber(ARGV[6]))
if not global or not user then return redis.error_reply('invalid token bucket state') end
local allowed = global.tokens >= 1 and user.tokens >= 1
local scope, retry = 0, 0
if allowed then
    global.tokens = global.tokens - 1
    user.tokens = user.tokens - 1
else
    -- 同时受限时提示用户维度；预计等待时间须满足两个桶。
    if user.tokens < 1 then scope = 2 else scope = 1 end
    local function wait(bucket)
        if bucket.tokens >= 1 then return 0 end
        return bucket.updated - now + (1 - bucket.tokens) * 1000 / bucket.rate
    end
    retry = math.max(1, math.ceil(math.max(wait(global), wait(user))))
    if retry > maxMillis then return redis.error_reply('token bucket retry time overflow') end
end

local function save(key, bucket)
    -- 保留小数令牌；拒绝时仅保存自然补充结果，不消耗任一桶。
    redis.call('HSET', key, 'tokens', string.format('%.17g', bucket.tokens),
        'updated_ms', string.format('%.0f', bucket.updated))
    -- 空闲至少经过完整恢复周期才回收，删除后重新满桶不会提前增加额度。
    redis.call('PEXPIRE', key, bucket.ttl)
end
save(KEYS[1], global)
save(KEYS[2], user)
if allowed then return {1, 0, 0} end
return {0, scope, retry}
