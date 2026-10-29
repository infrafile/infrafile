package main

import (
	"fmt"
	"time"

	"github.com/infrafile/infrafile/internal/plugin"
	"github.com/infrafile/infrafile/internal/validate"
	"github.com/spf13/cobra"
)

func newDoctorCmd(opts *globalOpts) *cobra.Command {
	var online bool
	var name, instance string
	var timeout time.Duration
	cmd := &cobra.Command{
		Use: "doctor [目录]", Short: "离线预检；显式选择实例后可做只读在线诊断",
		Args: maxArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if timeout <= 0 || timeout > 2*time.Minute || (name == "") != (instance == "") || online && name == "" {
				return usageError(cmd, "--online 需要 --plugin 和 --instance；--timeout 须大于 0 且不超过 2m")
			}
			res, err := validate.Run(cmd.Context(), dirArg(args), validate.Options{})
			if err != nil {
				return fmt.Errorf("无法开始预检；请检查数据仓路径、访问权限或取消状态")
			}
			check := plugin.DoctorCheck{Check: "repository", Status: "passed", Code: "valid", Message: "布局、结构、外键及明文凭据检查通过"}
			checks := []plugin.DoctorCheck{check}
			if len(res.Problems) > 0 {
				checks[0].Status, checks[0].Code, checks[0].Message = "failed", "invalid-repository", "数据仓校验未通过；请在本地使用 validate 定位问题"
			} else {
				instances, err := plugin.Doctor(cmd.Context(), res.Repo, name, instance, online, timeout)
				if err != nil {
					return err
				}
				checks = append(checks, instances...)
			}
			ok := true
			for _, check := range checks {
				ok = ok && check.Status == "passed"
			}
			if opts.json {
				if err := writeJSON(cmd.OutOrStdout(), struct {
					Version string               `json:"version"`
					OK      bool                 `json:"ok"`
					Online  bool                 `json:"online"`
					Checks  []plugin.DoctorCheck `json:"checks"`
				}{jsonVersion, ok, online, checks}); err != nil {
					return err
				}
			} else {
				for _, c := range checks {
					subject := c.Subject
					if c.Plugin != "" {
						subject = c.Plugin + "/" + c.Instance
					}
					if subject == "" {
						subject = "数据仓"
					}
					fmt.Fprintf(cmd.OutOrStdout(), "%s：%s [%s] %s\n", subject, c.Status, c.Code, c.Message)
					for _, permission := range c.Permissions {
						fmt.Fprintf(cmd.OutOrStdout(), "  所需权限（未完整验证）：%s\n", permission)
					}
				}
			}
			if !ok {
				return exitCode(1)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&online, "online", false, "仅对显式选定的内置实例执行只读采集，不写 state")
	cmd.Flags().StringVar(&name, "plugin", "", "启用的插件名；须与 --instance 同时提供")
	cmd.Flags().StringVar(&instance, "instance", "", "启用的实例名")
	cmd.Flags().DurationVar(&timeout, "timeout", 30*time.Second, "所选实例的总时限，最多 2m；HTTP 请求最多 20 次")
	return cmd
}
