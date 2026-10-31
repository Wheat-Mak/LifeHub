-- 本地联调用虚构店铺与消费券，固定 ID 已存在时不覆盖现有记录。
SET NAMES utf8mb4;
SET time_zone = '+00:00';

INSERT INTO tb_shop
    (id, name, type_id, images, area, address, x, y, avg_price, sold, comments, score, open_hours)
VALUES
    (1, 'LifeMate 示例茶餐厅', 1, '', '示例商圈', '示例路 1 号', 120.15, 30.31, 60, 0, 0, 45, '10:00-22:00'),
    (2, 'LifeMate 示例咖啡店', 2, '', '示例商圈', '示例路 2 号', 120.16, 30.32, 30, 0, 0, 46, '09:00-21:00')
ON DUPLICATE KEY UPDATE id = tb_shop.id;

-- 一个活动一种券，金额单位为分：80 元抵 100 元。
-- 首次导入时已开始、30 天后结束；重跑脚本不重置已扣库存或活动时间。
INSERT INTO tb_voucher
    (id, shop_id, title, sub_title, rules, pay_value, actual_value, status, stock, begin_time, end_time)
VALUES
    (2, 1, '100元秒杀代金券', '同步下单联调示例', '仅限堂食，不兑现、不找零', 8000, 10000, 1,
     100, UTC_TIMESTAMP() - INTERVAL 1 DAY, UTC_TIMESTAMP() + INTERVAL 30 DAY)
ON DUPLICATE KEY UPDATE id = tb_voucher.id;
