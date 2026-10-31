-- 按订单号去重。购买用户 Set 不删除，取消后也不能再次领取同一张券。
local markerType = redis.call('TYPE', KEYS[2]).ok
if markerType ~= 'none' and markerType ~= 'set' then
    return redis.error_reply('invalid restore marker type')
end
if redis.call('SISMEMBER', KEYS[2], ARGV[1]) == 1 then return 0 end
if redis.call('TYPE', KEYS[1]).ok ~= 'string' then
    return redis.error_reply('seckill stock missing or invalid')
end
local raw = redis.call('GET', KEYS[1])
local stock = tonumber(raw)
if not string.match(raw, '^%d+$') or not stock or stock >= 2147483647 then
    return redis.error_reply('invalid restore stock')
end
redis.call('INCR', KEYS[1])
redis.call('SADD', KEYS[2], ARGV[1])
return 1
