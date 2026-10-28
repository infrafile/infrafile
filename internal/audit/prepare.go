package audit

import "context"

// Prepare 只为所选规则解析业务观测，所有规则复用同一份已验证的快照。
func Prepare(ctx context.Context, in *Input, rules []Rule) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	lifecycleRules := []Rule{}
	for _, rule := range rules {
		switch rule.ID {
		case "orphan", "zombie":
			lifecycleRules = append(lifecycleRules, rule)
		case "origin_exposure":
			if err := prepareOrigin(ctx, in, rule); err != nil {
				return err
			}
		case "cdn_drift":
			if err := prepareCDN(ctx, in); err != nil {
				return err
			}
		}
	}
	if len(lifecycleRules) > 0 {
		return prepareLifecycle(ctx, in, lifecycleRules)
	}
	return nil
}
