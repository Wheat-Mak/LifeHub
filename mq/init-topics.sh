#!/bin/sh
set -eu
sh mqadmin updateTopic -n namesrv:9876 -b broker:10911 -t lifemate-seckill-orders-v2 -r 4 -w 4
sh mqadmin updateTopic -n namesrv:9876 -b broker:10911 -t lifemate-seckill-timeouts-v2 -r 4 -w 4 -a '+message.type=DELAY'
