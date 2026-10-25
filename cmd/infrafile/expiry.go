package main

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

type expiryEntry struct {
	Kind      string `json:"kind"`
	Name      string `json:"name"`
	File      string `json:"file"`
	ExpiresAt string `json:"expires_at"`
	DaysLeft  int    `json:"days_left"`
}

var dayWindow = regexp.MustCompile(`^[0-9]+d$`)

func newExpiryCmd(opts *globalOpts) *cobra.Command {
	var within string
	cmd := &cobra.Command{
		Use:   "expiry [目录]",
		Short: "检查在用记录的到期日",
		Long: `检查在用记录的 spec.expires_at，默认报告已过期和 30 天内（含边界）到期的记录。
按本地日历日计算；忽略归档记录和没有到期日的记录。

退出码：0 没有到期项，1 发现到期项，2 用法错误或无法检查。`,
		Args: maxArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			days, err := strconv.Atoi(strings.TrimSuffix(within, "d"))
			if !dayWindow.MatchString(within) || err != nil {
				return usageError(cmd, "--within 必须是非负整数天数，如 30d")
			}
			repo, err := loadQueryRepo(dirArg(args))
			if err != nil {
				return err
			}
			// 在 UTC 中比较日历日，避免夏令时让一天变成 23 或 25 小时。
			year, month, day := opts.now.Date()
			today := time.Date(year, month, day, 0, 0, 0, 0, time.UTC)
			entries := []expiryEntry{}
			for _, r := range repo.Records {
				if r.Archived() {
					continue
				}
				spec := r.Value.(map[string]any)["spec"].(map[string]any)
				raw, ok := spec["expires_at"].(string)
				if !ok {
					continue
				}
				expires, err := time.Parse(time.DateOnly, raw)
				if err != nil {
					return fmt.Errorf("%s: spec.expires_at 不是有效日期", r.File)
				}
				left := int((expires.Unix() - today.Unix()) / 86400)
				if left <= days {
					entries = append(entries, expiryEntry{r.Kind, r.Name, r.File, raw, left})
				}
			}
			sort.Slice(entries, func(i, j int) bool {
				if entries[i].DaysLeft != entries[j].DaysLeft {
					return entries[i].DaysLeft < entries[j].DaysLeft
				}
				return entries[i].File < entries[j].File
			})
			out := cmd.OutOrStdout()
			if opts.json {
				err = writeJSON(out, struct {
					Version    string        `json:"version"`
					OK         bool          `json:"ok"`
					AsOf       string        `json:"as_of"`
					WithinDays int           `json:"within_days"`
					Entries    []expiryEntry `json:"entries"`
				}{jsonVersion, len(entries) == 0, today.Format(time.DateOnly), days, entries})
			} else {
				var buf strings.Builder
				fmt.Fprintf(&buf, "截至 %s（阈值 %d 天）\n", today.Format(time.DateOnly), days)
				for _, e := range entries {
					status := fmt.Sprintf("剩余 %d 天", e.DaysLeft)
					if e.DaysLeft < 0 {
						status = fmt.Sprintf("已过期 %d 天", -e.DaysLeft)
					} else if e.DaysLeft == 0 {
						status = "今天到期"
					}
					fmt.Fprintf(&buf, "  %s/%s  %s  %s\n", e.Kind, e.Name, e.ExpiresAt, status)
				}
				fmt.Fprintf(&buf, "发现 %d 条到期记录\n", len(entries))
				_, err = fmt.Fprint(out, buf.String())
			}
			if err != nil {
				return err
			}
			if len(entries) > 0 {
				return exitCode(1)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&within, "within", "30d", "到期阈值（含边界），如 30d；0d 只看已过期和今天到期")
	return cmd
}
