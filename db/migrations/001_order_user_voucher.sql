-- 已存在的数据库执行一次。新建库已由 schema.sql 包含本约束，无需重复执行。
-- 若有历史重复订单，增加索引会失败；先检查并人工处理，不自动删除订单或调整库存。
-- SELECT user_id, voucher_id, COUNT(*) FROM tb_voucher_order
-- GROUP BY user_id, voucher_id HAVING COUNT(*) > 1;
ALTER TABLE tb_voucher_order
    ADD UNIQUE KEY uk_voucher_order_user_voucher (user_id, voucher_id);

ALTER TABLE tb_voucher ALTER COLUMN type SET DEFAULT 1;

-- 清理旧版脚本创建且未被订单引用的普通券示例，其他已有记录不删除。
DELETE FROM tb_voucher
WHERE id = 1 AND shop_id = 1 AND type = 0 AND title = '50元代金券'
    AND pay_value = 4750 AND actual_value = 5000
    AND NOT EXISTS (SELECT 1 FROM tb_voucher_order WHERE voucher_id = 1);
