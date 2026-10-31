-- 先停止旧服务并排空旧格式订单消息；仅在尚未包含这些字段的旧库执行一次。
-- 历史订单不伪造金额或期限，保留 NULL 时间；新支付拒绝无完整事实的历史订单。
SET SESSION lock_wait_timeout = 5;
ALTER TABLE tb_voucher_order
    ADD COLUMN amount BIGINT UNSIGNED NOT NULL DEFAULT 0 COMMENT '下单金额快照，分' AFTER voucher_id,
    ADD COLUMN reserved_at DATETIME(3) NULL DEFAULT NULL COMMENT 'Redis 预扣时间，UTC；历史订单为空' AFTER amount,
    ADD COLUMN expire_at DATETIME(3) NULL DEFAULT NULL COMMENT '固定支付截止时间，UTC；历史订单为空' AFTER reserved_at,
    MODIFY COLUMN pay_time TIMESTAMP(3) NULL DEFAULT NULL;
