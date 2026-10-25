package plugin

import (
	"context"
	"net/http"
	"net/url"
	"sort"
	"strconv"
)

type cfDNS struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Type      string `json:"type"`
	Content   string `json:"content"`
	TTL       int    `json:"ttl"`
	Priority  *int   `json:"priority,omitempty"`
	Proxied   bool   `json:"proxied"`
	Proxiable bool   `json:"proxiable"`
}

type cfZone struct {
	ID      string  `json:"id"`
	Name    string  `json:"name"`
	Status  string  `json:"status"`
	Paused  bool    `json:"paused"`
	Records []cfDNS `json:"dns_records"`
}

func (b *builtin) cloudflare(ctx context.Context, config map[string]any) (any, error) {
	var cfg struct {
		APIToken  string `json:"api_token"`
		AccountID string `json:"account_id"`
	}
	if err := decodeConfig(config, &cfg); err != nil {
		return nil, err
	}
	token, err := b.credential(ctx, cfg.APIToken)
	if err != nil {
		return nil, err
	}
	headers := http.Header{"Authorization": {"Bearer " + token}}
	query := url.Values{"type": {"full,partial,secondary,internal"}}
	if cfg.AccountID != "" {
		query.Set("account.id", cfg.AccountID)
	}
	zones, err := cfPages[cfZone](ctx, "/zones", query, headers)
	if err != nil {
		return nil, err
	}
	for i := range zones {
		if zones[i].ID == "" || zones[i].Name == "" {
			return nil, failure("protocol", "Cloudflare zone 缺少标识")
		}
		records, err := cfPages[cfDNS](ctx, "/zones/"+url.PathEscape(zones[i].ID)+"/dns_records", url.Values{}, headers)
		if err != nil {
			return nil, err
		}
		for _, record := range records {
			if record.ID == "" || record.Name == "" || record.Type == "" {
				return nil, failure("protocol", "Cloudflare DNS 记录缺少标识")
			}
		}
		sort.Slice(records, func(i, j int) bool {
			if records[i].Name != records[j].Name {
				return records[i].Name < records[j].Name
			}
			return records[i].ID < records[j].ID
		})
		zones[i].Records = records
	}
	sort.Slice(zones, func(i, j int) bool { return zones[i].Name < zones[j].Name })
	return map[string]any{"zones": zones}, nil
}

func cfPages[T any](ctx context.Context, path string, query url.Values, headers http.Header) ([]T, error) {
	all := []T{}
	for page := 1; ; page++ {
		query.Set("page", strconv.Itoa(page))
		query.Set("per_page", "50")
		var response struct {
			Success bool `json:"success"`
			Result  []T  `json:"result"`
			Info    struct {
				Page       int  `json:"page"`
				TotalCount *int `json:"total_count"`
			} `json:"result_info"`
		}
		if err := requestJSON(ctx, "GET", "https://api.cloudflare.com/client/v4"+path+"?"+query.Encode(), headers, nil, &response); err != nil {
			return nil, err
		}
		if !response.Success {
			return nil, failure("provider", "Cloudflare API 拒绝请求")
		}
		if response.Result == nil || response.Info.Page != page || response.Info.TotalCount == nil {
			return nil, failure("protocol", "Cloudflare 分页响应不完整")
		}
		all = append(all, response.Result...)
		if len(all) >= *response.Info.TotalCount {
			return all, nil
		}
		if len(response.Result) == 0 {
			return nil, failure("protocol", "Cloudflare 分页提前结束")
		}
	}
}
