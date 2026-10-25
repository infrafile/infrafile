package plugin

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"time"
)

func (b *builtin) ctyun(ctx context.Context, config map[string]any) (any, error) {
	auth, _, err := b.cloudConfig(ctx, config, []string{"cdn"})
	if err != nil {
		return nil, err
	}
	if auth.Token != "" {
		return nil, failure("config", "天翼 CDN 暂不支持 session_token")
	}
	all := []cdnEntry{}
	seen := map[string]bool{}
	for _, scope := range []int{1, 2, 3} {
		count := 0
		for page := 1; ; page++ {
			var response struct {
				Code       int  `json:"code"`
				Total      *int `json:"total"`
				TotalCount *int `json:"total_count"`
				Page       int  `json:"page"`
				Pages      int  `json:"page_count"`
				Result     []struct {
					Name   string `json:"domain"`
					CNAME  string `json:"cname"`
					Type   string `json:"product_code"`
					Status int    `json:"status"`
					Area   int    `json:"area_scope"`
				} `json:"result"`
			}
			query := url.Values{"area_scope": {strconv.Itoa(scope)}, "page": {strconv.Itoa(page)}, "page_size": {"100"}}
			if err := ctyunRequest(ctx, query, auth, &response); err != nil {
				return nil, err
			}
			if response.Code != 100000 {
				return nil, failure("provider", "天翼 CDN API 拒绝请求")
			}
			total := response.TotalCount
			if total == nil {
				total = response.Total
			}
			if (response.Page != 0 && response.Page != page) || response.Pages < 0 || (total != nil && *total < 0) || (response.Result == nil && (total == nil || *total != 0)) {
				return nil, failure("protocol", "天翼 CDN 分页响应不完整")
			}
			for _, item := range response.Result {
				if item.Name == "" {
					return nil, failure("protocol", "天翼 CDN 缺少名称")
				}
				key := item.Name + "\x00" + item.Type + "\x00" + strconv.Itoa(item.Area)
				if seen[key] {
					continue
				}
				seen[key] = true
				area := map[int]string{1: "domestic", 2: "overseas", 3: "global"}[item.Area]
				all = append(all, cdnEntry{Name: item.Name, CNAME: item.CNAME, Status: strconv.Itoa(item.Status), Type: item.Type, Area: area})
			}
			count += len(response.Result)
			done := len(response.Result) < 100
			if total != nil {
				done = count >= *total
			}
			if response.Pages > 0 {
				done = page >= response.Pages
			}
			if done {
				if total != nil && count < *total {
					return nil, failure("protocol", "天翼 CDN 清单未达到总数")
				}
				break
			}
			if len(response.Result) == 0 {
				return nil, failure("protocol", "天翼 CDN 分页提前结束")
			}
		}
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].Name != all[j].Name {
			return all[i].Name < all[j].Name
		}
		if all[i].Type != all[j].Type {
			return all[i].Type < all[j].Type
		}
		return all[i].Area < all[j].Area
	})
	return map[string]any{"cdn": all, "unsupported": []string{"registered-domains"}}, nil
}

func ctyunRequest(ctx context.Context, query url.Values, auth cloudAuth, result any) error {
	id := make([]byte, 16)
	if _, err := rand.Read(id); err != nil {
		return failure("http", "无法生成请求标识")
	}
	id[6] = (id[6] & 0x0f) | 0x40
	id[8] = (id[8] & 0x3f) | 0x80
	nonce := fmt.Sprintf("%x-%x-%x-%x-%x", id[:4], id[4:6], id[6:8], id[8:10], id[10:])
	// 官方 EOP SDK 使用北京时间，Z 在此格式中只是字面量。
	date := time.Now().In(time.FixedZone("CST", 8*3600)).Format("20060102T150405Z")
	headers := http.Header{}
	headers.Set("Ctyun-Eop-Request-Id", nonce)
	headers.Set("Eop-Date", date)
	encoded := query.Encode()
	headers.Set("Eop-Authorization", eopAuthorization(nonce, date, encoded, auth))
	return requestJSON(ctx, "GET", "https://cdnapi-global.ctapi.ctyun.cn/domain/query-domain-list?"+encoded, headers, nil, result)
}

func eopAuthorization(nonce, date, query string, auth cloudAuth) string {
	toSign := "ctyun-eop-request-id:" + nonce + "\neop-date:" + date + "\n\n" + query + "\n" + sha256Hex(nil)
	key := hmacSHA256([]byte(auth.Secret), date)
	key = hmacSHA256(key, auth.ID)
	key = hmacSHA256(key, date[:8])
	signature := base64.StdEncoding.EncodeToString(hmacSHA256(key, toSign))
	return auth.ID + " Headers=ctyun-eop-request-id;eop-date Signature=" + signature
}
