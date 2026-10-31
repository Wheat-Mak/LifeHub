package redisstore

import (
	"context"

	"github.com/redis/go-redis/v9"
)

// NoRetryScripts 禁止在执行结果不确定时自动重放 EVAL/EVALSHA。
// 若切换代次的响应丢失，重放同一随机值可能覆盖后来者的代次，产生 ABA。
// 秒杀脚本同样使用此入口，避免预扣已成功但响应丢失时，重放被误判为重复下单。
// NOSCRIPT 明确表示脚本未执行，仍允许 Script.Run 正常回退到 EVAL。
func NoRetryScripts(client *redis.Client) redis.Scripter {
	return noRetryScripter{client}
}

type noRetryScripter struct{ *redis.Client }

func (s noRetryScripter) Eval(ctx context.Context, source string, keys []string, args ...interface{}) *redis.Cmd {
	return s.run(ctx, "eval", source, keys, args...)
}

func (s noRetryScripter) EvalSha(ctx context.Context, hash string, keys []string, args ...interface{}) *redis.Cmd {
	return s.run(ctx, "evalsha", hash, keys, args...)
}

type noRetryCmd struct{ *redis.Cmd }

func (*noRetryCmd) NoRetry() bool { return true }

func (s noRetryScripter) run(ctx context.Context, command, script string, keys []string, args ...interface{}) *redis.Cmd {
	values := make([]interface{}, 0, 3+len(keys)+len(args))
	values = append(values, command, script, len(keys))
	for _, key := range keys {
		values = append(values, key)
	}
	values = append(values, args...)
	cmd := &noRetryCmd{redis.NewCmd(ctx, values...)}
	_ = s.Client.Process(ctx, cmd)
	return cmd.Cmd
}
