package control

import (
	"context"
	"encoding/json"
	"errors"
	"runtime"
	"time"

	"github.com/infrafile/infrafile/internal/ledger"
	"github.com/infrafile/infrafile/internal/plugin"
	"github.com/infrafile/infrafile/internal/validate"
	"github.com/infrafile/infrafile/protocol"
)

type ExecutionOptions struct {
	ReviewedDigest   string
	AuthorizationRef string
	Actor            string
	Now              time.Time
}

func freshBinding(ctx context.Context, dir string, p *Plan) (*ledger.Repo, error) {
	v, err := validate.Run(ctx, dir, validate.Options{})
	if err != nil {
		return nil, err
	}
	if len(v.Problems) > 0 || !managed(v.Repo) {
		return nil, errors.New("执行须通过当前台账校验并显式启用 storageVersion: v1")
	}
	a := p.Actions[0]
	b, c, rh, err := Resolve(v.Repo, a.Target)
	if err != nil || b != a.Binding || rh != a.RecordHash || plugin.Hash(c) != a.ConfigHash {
		return nil, errors.New("台账目标或插件配置已变化，请重新生成计划")
	}
	return v.Repo, nil
}

func planCurrent(p *Plan, now time.Time) error {
	generated, _ := time.Parse(time.RFC3339, p.GeneratedAt)
	expires, _ := time.Parse(time.RFC3339, p.ExpiresAt)
	if now.Before(generated) || !now.Before(expires) {
		return errors.New("计划尚未生效或已超过 30 分钟有效期，请重新生成并审阅")
	}
	return nil
}

// Execute journals a v2 attempt before control. A repeated digest only probes; it never writes again.
func Execute(ctx context.Context, repo *ledger.Repo, p *Plan, opts ExecutionOptions) (Result, error) {
	if runtime.GOOS == "windows" {
		return Result{}, errors.New("Windows 尚未验证执行回执权限与持久化；未创建执行目录，请使用 Linux/macOS")
	}
	if err := p.Validate(); err != nil {
		return Result{}, err
	}
	if p.Version != "v2" || !managed(repo) || opts.ReviewedDigest != p.Digest || !attributionText(opts.Actor, 128) || !attributionText(opts.AuthorizationRef, 256) {
		return Result{}, errors.New("v2 执行须匹配 --reviewed-digest，并提供 --authorization-ref 和 --actor；这些说明不构成身份认证")
	}
	attribution, _ := json.Marshal(map[string]any{"actor": opts.Actor, "authorization_ref": opts.AuthorizationRef})
	if _, err := plugin.Object(attribution); err != nil {
		return Result{}, errors.New("主体或授权引用包含疑似秘密，拒绝记录")
	}
	started := time.Now()
	now := func() time.Time { return opts.Now.Add(time.Since(started)).UTC() }
	// Reload before creating history so a removed storage marker never creates management records.
	repo, err := freshBinding(ctx, repo.Dir, p)
	if err != nil {
		return Result{}, err
	}
	store, err := openReceiptStore(ctx, repo.Dir)
	if err != nil {
		return Result{}, err
	}
	defer store.close()
	old, err := receiptBytes(store.root, p.Digest+".json")
	if err != nil {
		return Result{}, err
	}
	if old != nil {
		r, err := decodeReceipt(ctx, old)
		if err != nil {
			return Result{}, err
		}
		if r.Data.ID != p.Digest {
			return Result{}, errors.New("执行回执身份不符")
		}
		return verifyRepeated(ctx, repo, p, r)
	}
	if err := planCurrent(p, now()); err != nil {
		return Result{}, err
	}
	expires, _ := time.Parse(time.RFC3339, p.ExpiresAt)
	executionCtx, stop := context.WithTimeout(ctx, expires.Sub(now()))
	defer stop()
	view, err := listReceipts(ctx, store.root, "")
	if err != nil {
		return Result{}, err
	}
	if len(view.Executions) >= maxReceipts {
		return Result{}, errors.New("执行回执达到 256 条，请先独立归档；不会自动删除")
	}
	at := now().Format(time.RFC3339Nano)
	r := &Receipt{Version: "v1", Kind: "ExecutionReceipt", Data: ReceiptData{ID: p.Digest, Plan: p, Actor: opts.Actor, AuthorizationRef: opts.AuthorizationRef, CreatedAt: at, UpdatedAt: at, Phase: "prepared", Result: Result{Version: "v1", Target: p.Actions[0].Target, Status: "uncertain", Receipt: p.Digest, Message: "执行尚未完整记录；必须先回读核实，不可盲重试"}}}
	old, err = store.save(executionCtx, r, nil)
	if err != nil {
		return Result{}, errors.New("执行前回执无法持久保存，未发送控制请求")
	}
	result, applyErr := apply(executionCtx, repo, p, func() error {
		if err := planCurrent(p, now()); err != nil {
			return err
		}
		if _, err := freshBinding(executionCtx, repo.Dir, p); err != nil {
			return err
		}
		r.Data.Phase = "write_started"
		r.Data.UpdatedAt = now().Format(time.RFC3339Nano)
		r.Data.WriteStartedAt = r.Data.UpdatedAt
		r.Data.BeforeHash = p.Actions[0].BeforeHash
		var err error
		old, err = store.save(executionCtx, r, old)
		if err != nil {
			return errors.New("写前回执无法持久保存，未发送控制请求")
		}
		if err := planCurrent(p, now()); err != nil {
			return err
		}
		return executionCtx.Err()
	})
	result.Receipt = p.Digest
	if applyErr != nil {
		result.OK = false
		result.Status = "rejected"
		result.Message = "执行前提失败；未发送控制请求"
	}
	r.Data.Phase = "completed"
	r.Data.UpdatedAt = now().Format(time.RFC3339Nano)
	r.Data.BeforeHash = result.beforeHash
	r.Data.ReadbackHash = result.afterHash
	r.Data.Result = result
	if result.Status == "unchanged" {
		r.Data.ReadbackHash = result.beforeHash
	}
	// Cancellation after a cloud request must not erase the durable uncertain marker.
	finalCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if _, err := store.save(finalCtx, r, old); err != nil {
		return Result{Version: "v1", Target: p.Actions[0].Target, Status: "uncertain", Receipt: p.Digest, Message: "执行回执未确认保存；远端可能已改变。先核实已有回执与远端，不要盲重试"}, nil
	}
	return result, applyErr
}

func verifyRepeated(ctx context.Context, repo *ledger.Repo, p *Plan, receipt *Receipt) (Result, error) {
	r := receipt.Data.Result
	r.Replayed = true
	r.OK = false
	r.Verification = "unavailable"
	if receipt.Data.Result.OK {
		r.Status = "uncertain"
	}
	b, c, _, err := Resolve(repo, p.Actions[0].Target)
	if err != nil {
		return r, err
	}
	client, err := plugin.Open(ctx, b.Plugin, repo.Dir)
	if err != nil {
		r.Message = "已有回执，本次只读核实失败；未重发控制请求"
		return r, nil
	}
	defer client.Close()
	state, err := observe(ctx, client, protocol.ControlParams{Instance: b.Instance, Config: c, Target: b.Target, Action: p.Actions[0].Operation, Parameters: p.Actions[0].Parameters})
	if err != nil {
		r.Message = "已有回执，本次只读回读失败；未重发控制请求"
		return r, nil
	}
	r.ObservedHash = plugin.Hash(state)
	r.Verification = "different"
	if r.ObservedHash == plugin.Hash(p.Actions[0].After) {
		r.Verification = "matches_expected"
		r.OK = receipt.Data.Result.OK
		r.Status = receipt.Data.Result.Status
	}
	if r.Verification == "different" && r.ObservedHash == p.Actions[0].BeforeHash {
		r.Verification = "matches_before"
	}
	r.Message = "已有回执，本次仅回读核实；保留原执行结论，未重发控制请求。核实不证明原请求成功"
	return r, nil
}
