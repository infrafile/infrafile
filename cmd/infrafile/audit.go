package main

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/infrafile/infrafile/internal/audit"
	"github.com/spf13/cobra"
)

type auditSnapshot struct {
	Plugin     string  `json:"plugin"`
	Instance   string  `json:"instance"`
	File       string  `json:"file"`
	ObservedAt *string `json:"observed_at"`
	AgeDays    *int    `json:"age_days"`
}

func newAuditCmd(opts *globalOpts) *cobra.Command {
	var maxAge string
	var ruleIDs []string
	cmd := &cobra.Command{
		Use:   "audit [目录]",
		Short: "对照台账和插件快照做只读审计",
		Long: `对照台账和 state/ 下已启用插件实例的快照做只读审计，不访问网络，不写文件。
不加 --rule 时运行全部已就绪的规则；stale_state 和 missing 需要 --max-age。
missing 检查 audit.watch 声明的范围，需要同一绑定的正向基线和新鲜成功观测；不表示资产已删除。
orphan 检查未登记资产，zombie 检查仅匹配归档记录的资产；按自然身份匹配，DNS/TLS 不参与。
origin_exposure 对照台账 DNS、Cloudflare 和 DNS 快照，检查显式绑定 Server 的主机名是否直连源站。
cdn_drift 检查阿里云、腾讯云、天翼云 CDN 的明确停用状态；未知值只提示，不推断漂移。
metadata.waivers 和 infrafile.yaml 的 audit.ignore 可以豁免偏差，过期或未命中的豁免会给出提示。

退出码：0 没有未豁免的偏差，1 发现未豁免的偏差，2 用法错误或无法审计。`,
		Args: maxArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			in := &audit.Input{Now: opts.now}
			if cmd.Flags().Changed("max-age") {
				days, err := strconv.Atoi(strings.TrimSuffix(maxAge, "d"))
				if !dayWindow.MatchString(maxAge) || err != nil {
					return usageError(cmd, "--max-age 必须是非负整数天数，如 7d")
				}
				in.MaxAge = &days
			}
			rules, err := audit.Select(ruleIDs, in)
			if err != nil {
				return usageError(cmd, err.Error())
			}
			if in.Repo, err = loadQueryRepo(cmd.Context(), dirArg(args)); err != nil {
				return err
			}
			snapshots, notices, err := audit.ReadSnapshots(cmd.Context(), in.Repo)
			if err != nil {
				return err
			}
			in.Snapshots = snapshots
			if err := audit.Prepare(cmd.Context(), in, rules); err != nil {
				return err
			}
			findings, more := audit.Run(in, rules)
			history, err := audit.ReadHistoryView(cmd.Context(), in.Repo.Dir, nil)
			if err != nil {
				return err
			}
			notices = append(notices, more...)
			open := 0
			for _, f := range findings {
				if f.Waiver == nil {
					open++
				}
			}

			out := cmd.OutOrStdout()
			if opts.json {
				ids, snaps := []string{}, []auditSnapshot{}
				for _, r := range rules {
					ids = append(ids, r.ID)
				}
				for _, s := range snapshots {
					snap := auditSnapshot{Plugin: s.Plugin, Instance: s.Instance, File: s.File, AgeDays: s.AgeDays(in.Now)}
					if s.ObservedAt != nil {
						at := s.ObservedAt.UTC().Format(time.RFC3339)
						snap.ObservedAt = &at
					}
					snaps = append(snaps, snap)
				}
				err = writeJSON(out, struct {
					Version    string             `json:"version"`
					OK         bool               `json:"ok"`
					AsOf       string             `json:"as_of"`
					MaxAgeDays *int               `json:"max_age_days"`
					Rules      []string           `json:"rules"`
					Snapshots  []auditSnapshot    `json:"snapshots"`
					Findings   []audit.Finding    `json:"findings"`
					Notices    []audit.Notice     `json:"notices"`
					History    *audit.HistoryView `json:"history,omitempty"`
				}{jsonVersion, open == 0, in.Now.UTC().Format(time.RFC3339), in.MaxAge, ids, snaps, findings, notices, history})
			} else {
				var buf strings.Builder
				limit := "未设观测上限"
				if in.MaxAge != nil {
					limit = fmt.Sprintf("观测上限 %d 天", *in.MaxAge)
				}
				fmt.Fprintf(&buf, "截至 %s（%s）\n", in.Now.UTC().Format(time.RFC3339), limit)
				fmt.Fprintf(&buf, "快照 %d 份\n", len(snapshots))
				for _, s := range snapshots {
					age := "无同步记录"
					if days := s.AgeDays(in.Now); days != nil {
						age = fmt.Sprintf("%s 观测，%d 天前", s.ObservedAt.UTC().Format(time.RFC3339), *days)
					}
					fmt.Fprintf(&buf, "  %s/%s  %s\n", s.Plugin, s.Instance, age)
				}
				if len(rules) == 0 {
					buf.WriteString("没有可运行的规则；加 --max-age 7d 检查快照是否过旧\n")
				}
				for _, r := range rules {
					fmt.Fprintf(&buf, "%s：%s\n", r.ID, r.Description)
					clean := true
					for _, f := range findings {
						if f.Rule != r.ID {
							continue
						}
						clean = false
						fmt.Fprintf(&buf, "  %s  %s  %s\n", f.Target, f.File, f.Message)
						if w := f.Waiver; w != nil {
							until := ""
							if w.Until != "" {
								until = "至 " + w.Until + "，"
							}
							fmt.Fprintf(&buf, "    [已豁免] %s（%s来源 %s）\n", w.Reason, until, w.Source)
						}
					}
					if clean {
						buf.WriteString("  无偏差\n")
					}
				}
				if len(notices) > 0 {
					buf.WriteString("提示：\n")
					for _, n := range notices {
						rule := ""
						if n.Rule != "" {
							rule = n.Rule + " "
						}
						fmt.Fprintf(&buf, "  %s%s  %s  %s\n", rule, n.Target, n.File, n.Message)
					}
				}
				fmt.Fprintf(&buf, "发现 %d 条偏差，%d 条已豁免\n", open, len(findings)-open)
				if history != nil {
					fmt.Fprintf(&buf, "已保存历史：%s 检查，%d 条问题（本次查询未推进）\n", history.CheckedAt, len(history.Issues))
				}
				_, err = fmt.Fprint(out, buf.String())
			}
			if err != nil {
				return err
			}
			if open > 0 {
				return exitCode(1)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&maxAge, "max-age", "", "快照观测时间上限，如 7d；设置后运行 stale_state 和 missing")
	cmd.Flags().StringArrayVar(&ruleIDs, "rule", nil, "只运行指定规则，可重复")
	return cmd
}
