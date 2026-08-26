package kernel

import "context"

type callIDCtxKey struct{}

// WithCallID 把当前工具调用 ID 放入 ctx（执行器调用），
// 交互工具/审批策略用它作为答复的关联键。
func WithCallID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, callIDCtxKey{}, id)
}

// CallIDFromCtx 取出当前工具调用 ID；不存在返回 ""。
func CallIDFromCtx(ctx context.Context) string {
	id, _ := ctx.Value(callIDCtxKey{}).(string)
	return id
}
