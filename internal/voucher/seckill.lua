-- KEYS: 详情、库存、购买用户、订单 Hash；ARGV: userId/orderId/期限毫秒/宽限毫秒/voucherId。
local detailType = redis.call('TYPE', KEYS[1]).ok
local stockType = redis.call('TYPE', KEYS[2]).ok
local buyersType = redis.call('TYPE', KEYS[3]).ok
if detailType == 'none' or stockType == 'none' then return {3} end
if detailType ~= 'string' or stockType ~= 'string' or
   (buyersType ~= 'none' and buyersType ~= 'set') then
    return redis.error_reply('invalid seckill key type')
end
local valid, detail = pcall(cjson.decode, redis.call('GET', KEYS[1]))
if not valid or type(detail) ~= 'table' or type(detail.beginUnix) ~= 'number' or
   type(detail.endUnix) ~= 'number' or detail.beginUnix >= detail.endUnix then
    return redis.error_reply('invalid seckill detail')
end
if detail.status ~= 1 then return {4} end
local clock = redis.call('TIME')
local now = tonumber(clock[1])
if now < detail.beginUnix then return {5} end
if now >= detail.endUnix then return {6} end
local rawStock = redis.call('GET', KEYS[2])
local stock = tonumber(rawStock)
if not string.match(rawStock, '^%d+$') or not stock or stock > 2147483647 then
    return redis.error_reply('invalid seckill stock')
end
if redis.call('SISMEMBER', KEYS[3], ARGV[1]) == 1 then return {2} end
if stock <= 0 then return {1} end
if redis.call('TYPE', KEYS[4]).ok ~= 'none' then
    return redis.error_reply('preorder id already exists')
end
if type(detail.payValue) ~= 'number' or detail.payValue < 0 or
   detail.payValue > 9007199254740991 or detail.payValue ~= math.floor(detail.payValue) then
    return redis.error_reply('invalid payment amount')
end
local reserved = now * 1000 + math.floor(tonumber(clock[2]) / 1000)
local expire = reserved + tonumber(ARGV[3])
local amountText = string.format('%.0f', detail.payValue)
local reservedText = string.format('%.0f', reserved)
local expireText = string.format('%.0f', expire)
redis.call('DECR', KEYS[2])
redis.call('SADD', KEYS[3], ARGV[1])
redis.call('HSET', KEYS[4], 'userId', ARGV[1], 'voucherId', ARGV[5],
    'amount', amountText, 'reservedAt', reservedText, 'expireAt', expireText)
redis.call('PEXPIREAT', KEYS[4], expire + tonumber(ARGV[4]))
return {0, amountText, reservedText, expireText}
