package plugin

import (
	"context"
	"encoding/hex"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

func (b *builtin) baidu(ctx context.Context, config map[string]any) (any, error) {
	auth, services, err := b.cloudConfig(ctx, config, []string{"domains", "cdn"})
	if err != nil {
		return nil, err
	}
	result := map[string]any{}
	for _, service := range services {
		if service == "domains" {
			result[service], err = baiduDomains(ctx, auth)
		} else {
			result[service], err = baiduCDN(ctx, auth)
		}
		if err != nil {
			return nil, err
		}
	}
	return result, nil
}

func baiduDomains(ctx context.Context, auth cloudAuth) ([]domainEntry, error) {
	all := []domainEntry{}
	for page := 1; ; page++ {
		var response struct {
			Code   string `json:"code"`
			Total  *int   `json:"totalCount"`
			Result []struct {
				Name      string `json:"domainName"`
				ID        string `json:"resourceId"`
				Status    string `json:"domainStatus"`
				ExpiresAt string `json:"expireTime"`
			} `json:"result"`
		}
		if err := baiduRequest(ctx, "bcd.baidubce.com", url.Values{"pageNo": {strconv.Itoa(page)}, "pageSize": {"100"}}, auth, &response); err != nil {
			return nil, err
		}
		if response.Code != "" {
			return nil, failure("provider", "百度域名 API 拒绝请求")
		}
		if response.Total == nil || *response.Total < 0 || response.Result == nil {
			return nil, failure("protocol", "百度域名分页响应不完整")
		}
		for _, item := range response.Result {
			if item.Name == "" {
				return nil, failure("protocol", "百度域名缺少名称")
			}
			date, err := dateOnly(item.ExpiresAt)
			if err != nil {
				return nil, err
			}
			all = append(all, domainEntry{Name: item.Name, ID: item.ID, Status: item.Status, ExpiresAt: date})
		}
		if len(all) >= *response.Total {
			break
		}
		if len(response.Result) == 0 {
			return nil, failure("protocol", "百度域名分页提前结束")
		}
	}
	sort.Slice(all, func(i, j int) bool { return all[i].Name < all[j].Name })
	return all, nil
}

func baiduCDN(ctx context.Context, auth cloudAuth) ([]cdnEntry, error) {
	all := []cdnEntry{}
	marker := ""
	seen := map[string]bool{}
	for {
		var response struct {
			Code    string `json:"code"`
			Domains []struct {
				Name string `json:"name"`
			} `json:"domains"`
			Truncated *bool  `json:"isTruncated"`
			Next      string `json:"nextMarker"`
		}
		query := url.Values{"maxItems": {"200"}}
		if marker != "" {
			query.Set("marker", marker)
		}
		if err := baiduRequest(ctx, "cdn.baidubce.com", query, auth, &response); err != nil {
			return nil, err
		}
		if response.Code != "" {
			return nil, failure("provider", "百度 CDN API 拒绝请求")
		}
		if response.Domains == nil || response.Truncated == nil {
			return nil, failure("protocol", "百度 CDN 分页响应不完整")
		}
		for _, item := range response.Domains {
			if item.Name == "" {
				return nil, failure("protocol", "百度 CDN 缺少名称")
			}
			all = append(all, cdnEntry{Name: item.Name})
		}
		if !*response.Truncated {
			break
		}
		if len(response.Domains) == 0 || response.Next == "" || seen[response.Next] {
			return nil, failure("protocol", "百度 CDN 游标没有前进")
		}
		marker = response.Next
		seen[marker] = true
	}
	sort.Slice(all, func(i, j int) bool { return all[i].Name < all[j].Name })
	return all, nil
}

func baiduRequest(ctx context.Context, host string, query url.Values, auth cloudAuth, result any) error {
	headers := http.Header{}
	headers.Set("Host", host)
	headers.Set("X-Bce-Date", time.Now().UTC().Format(time.RFC3339))
	if auth.Token != "" {
		headers.Set("X-Bce-Security-Token", auth.Token)
	}
	encoded := rfc3986(query.Encode())
	headers.Set("Authorization", bceAuthorization("GET", "/v2/domain", encoded, headers, auth))
	return requestJSON(ctx, "GET", "https://"+host+"/v2/domain?"+encoded, headers, nil, result)
}

func bceAuthorization(method, path, query string, headers http.Header, auth cloudAuth) string {
	keys := []string{}
	for key := range headers {
		keys = append(keys, strings.ToLower(key))
	}
	sort.Strings(keys)
	canonical := make([]string, 0, len(keys))
	for _, key := range keys {
		canonical = append(canonical, rfc3986(url.QueryEscape(key))+":"+rfc3986(url.QueryEscape(strings.TrimSpace(headers.Get(key)))))
	}
	segments := strings.Split(path, "/")
	for i, segment := range segments {
		segments[i] = rfc3986(url.QueryEscape(segment))
	}
	request := method + "\n" + strings.Join(segments, "/") + "\n" + query + "\n" + strings.Join(canonical, "\n")
	prefix := "bce-auth-v1/" + auth.ID + "/" + headers.Get("X-Bce-Date") + "/1800"
	key := hex.EncodeToString(hmacSHA256([]byte(auth.Secret), prefix))
	signature := hex.EncodeToString(hmacSHA256([]byte(key), request))
	return prefix + "/" + strings.Join(keys, ";") + "/" + signature
}
