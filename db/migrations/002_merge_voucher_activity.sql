-- 旧版已执行 001 后执行一次。新建库直接使用 schema.sql，不执行历史迁移。
-- 执行时停止旧服务写入。仅允许每张券恰有一个活动、没有孤立活动的旧数据。
-- 不符合条件时 CHECK 失败，需先人工核对，不能继续执行后面的 DDL。
CREATE TEMPORARY TABLE lifemate_voucher_merge_guard (
    valid TINYINT NOT NULL CHECK (valid = 1)
);
INSERT INTO lifemate_voucher_merge_guard (valid)
SELECT IF(
    EXISTS (SELECT 1 FROM tb_voucher v LEFT JOIN tb_seckill_voucher sv ON sv.voucher_id = v.id
            WHERE v.type <> 1 OR sv.voucher_id IS NULL)
    OR EXISTS (SELECT 1 FROM tb_seckill_voucher sv LEFT JOIN tb_voucher v ON v.id = sv.voucher_id
               WHERE v.id IS NULL), 0, 1);

ALTER TABLE tb_voucher
    ADD COLUMN stock INT NULL,
    ADD COLUMN begin_time TIMESTAMP NULL DEFAULT NULL,
    ADD COLUMN end_time TIMESTAMP NULL DEFAULT NULL;

UPDATE tb_voucher v JOIN tb_seckill_voucher sv ON sv.voucher_id = v.id
SET v.stock = sv.stock, v.begin_time = sv.begin_time, v.end_time = sv.end_time,
    v.update_time = GREATEST(v.update_time, sv.update_time);

ALTER TABLE tb_voucher
    MODIFY COLUMN stock INT NOT NULL COMMENT '剩余库存，仅由 MySQL 事务扣减',
    MODIFY COLUMN begin_time TIMESTAMP NOT NULL,
    MODIFY COLUMN end_time TIMESTAMP NOT NULL,
    DROP COLUMN type;

DROP TABLE tb_seckill_voucher;
DROP TEMPORARY TABLE lifemate_voucher_merge_guard;
