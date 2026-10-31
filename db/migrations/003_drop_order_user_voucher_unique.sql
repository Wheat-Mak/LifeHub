-- 已有联合唯一索引的旧库执行一次；新建库使用当前 schema.sql，无需执行。
-- 保留 PRIMARY KEY (id) 处理相同订单号的重复消息；一人一券一单由 Redis Lua 保证。
-- 仅删除索引，不修改订单字段、订单记录或库存。
SET SESSION lock_wait_timeout = 5;
ALTER TABLE tb_voucher_order
    DROP INDEX uk_voucher_order_user_voucher,
    ALGORITHM=INPLACE,
    LOCK=NONE;
