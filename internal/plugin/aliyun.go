package plugin

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

func (b *builtin) aliyun(ctx context.Context, config map[string]any) (any, error) {
	auth, services, err := b.cloudConfig(ctx, config, []string{"domains", "cdn"})
	if err != nil {
		return nil, err
	}
	result := map[string]any{}
	for _, service := range services {
		if service == "domains" {
			result[service], err = aliyunDomains(ctx, auth)
		} else {
			var entries []cdnEntry
			entries, err = aliyunCDN(ctx, auth)
			if err == nil && config["collect_origin"] == true {
				err = collectCDNOrigins(ctx, auth, entries, aliyunOrigin)
			}
			result[service] = entries
		}
		if err != nil {
			return nil, err
		}
	}
	return result, nil
}

func aliyunDomains(ctx context.Context, auth cloudAuth) ([]domainEntry, error) {
	all := []domainEntry{}
	for page := 1; ; page++ {
		var response struct {
			Code     string `json:"Code"`
			NextPage *bool  `json:"NextPage"`
			Total    *int   `json:"TotalItemNum"`
			Data     struct {
				Domains []struct {
					Name       string `json:"DomainName"`
					ID         string `json:"InstanceId"`
					Expiration string `json:"ExpirationDate"`
					Millis     int64  `json:"ExpirationDateLong"`
				} `json:"Domain"`
			} `json:"Data"`
		}
		query := url.Values{"PageNum": {strconv.Itoa(page)}, "PageSize": {"10"}}
		if err := aliyunRequest(ctx, "domain.aliyuncs.com", "QueryDomainList", "2018-01-29", query, auth, &response); err != nil {
			return nil, err
		}
		if response.Code != "" {
			return nil, failure("provider", "阿里云域名 API 拒绝请求")
		}
		if response.Data.Domains == nil || response.NextPage == nil || response.Total == nil || *response.Total < 0 {
			return nil, failure("protocol", "阿里云域名分页响应不完整")
		}
		for _, domain := range response.Data.Domains {
			if domain.Name == "" {
				return nil, failure("protocol", "阿里云域名缺少名称")
			}
			date := ""
			var err error
			if domain.Millis > 0 {
				// 与 ExpirationDate 字符串一致按北京时间取日期，UTC 会在 0–8 点早一天。
				date = time.UnixMilli(domain.Millis).In(time.FixedZone("CST", 8*3600)).Format("2006-01-02")
			} else {
				date, err = dateOnly(domain.Expiration)
			}
			if err != nil {
				return nil, err
			}
			all = append(all, domainEntry{Name: domain.Name, ID: domain.ID, ExpiresAt: date})
		}
		if !*response.NextPage {
			if len(all) != *response.Total {
				return nil, failure("protocol", "阿里云域名总数与清单不符")
			}
			break
		}
		if len(response.Data.Domains) == 0 {
			return nil, failure("protocol", "阿里云域名分页提前结束")
		}
	}
	sort.Slice(all, func(i, j int) bool { return all[i].Name < all[j].Name })
	return all, nil
}

func aliyunCDN(ctx context.Context, auth cloudAuth) ([]cdnEntry, error) {
	all := []cdnEntry{}
	for page := 1; ; page++ {
		var response struct {
			Code    string `json:"Code"`
			Total   *int   `json:"TotalCount"`
			Domains struct {
				Items []struct {
					Name   string      `json:"DomainName"`
					ID     json.Number `json:"DomainId"`
					CNAME  string      `json:"Cname"`
					Status string      `json:"DomainStatus"`
					Type   string      `json:"CdnType"`
					Area   string      `json:"Coverage"`
				} `json:"PageData"`
			} `json:"Domains"`
		}
		query := url.Values{"PageNumber": {strconv.Itoa(page)}, "PageSize": {"100"}, "CheckDomainShow": {"true"}}
		if err := aliyunRequest(ctx, "cdn.aliyuncs.com", "DescribeUserDomains", "2018-05-10", query, auth, &response); err != nil {
			return nil, err
		}
		if response.Code != "" {
			return nil, failure("provider", "阿里云 CDN API 拒绝请求")
		}
		if response.Domains.Items == nil || response.Total == nil || *response.Total < 0 {
			return nil, failure("protocol", "阿里云 CDN 分页响应不完整")
		}
		for _, item := range response.Domains.Items {
			if item.Name == "" {
				return nil, failure("protocol", "阿里云 CDN 缺少名称")
			}
			all = append(all, cdnEntry{Name: item.Name, ID: item.ID.String(), CNAME: item.CNAME, Status: item.Status, Type: item.Type, Area: item.Area})
		}
		if len(all) >= *response.Total {
			break
		}
		if len(response.Domains.Items) == 0 {
			return nil, failure("protocol", "阿里云 CDN 分页提前结束")
		}
	}
	sort.Slice(all, func(i, j int) bool { return all[i].Name < all[j].Name })
	return all, nil
}

func aliyunRequest(ctx context.Context, host, action, version string, query url.Values, auth cloudAuth, result any) error {
	headers := http.Header{}
	headers.Set("Host", host)
	headers.Set("Accept", "application/json")
	headers.Set("X-Acs-Action", action)
	headers.Set("X-Acs-Version", version)
	headers.Set("X-Acs-Date", time.Now().UTC().Format(time.RFC3339))
	headers.Set("X-Acs-Signature-Nonce", rand.Text())
	headers.Set("X-Acs-Content-Sha256", sha256Hex(nil))
	if auth.Token != "" {
		headers.Set("X-Acs-Security-Token", auth.Token)
	}
	encoded := rfc3986(query.Encode())
	headers.Set("Authorization", acs3Authorization(headers, encoded, auth))
	return requestJSON(ctx, "POST", "https://"+host+"/?"+encoded, headers, nil, result)
}

func acs3Authorization(headers http.Header, query string, auth cloudAuth) string {
	keys := []string{}
	for key := range headers {
		lower := strings.ToLower(key)
		if lower == "host" || lower == "content-type" || strings.HasPrefix(lower, "x-acs-") {
			keys = append(keys, lower)
		}
	}
	sort.Strings(keys)
	canonical := ""
	for _, key := range keys {
		canonical += key + ":" + strings.TrimSpace(headers.Get(key)) + "\n"
	}
	signed := strings.Join(keys, ";")
	request := "POST\n/\n" + query + "\n" + canonical + "\n" + signed + "\n" + sha256Hex(nil)
	signature := hex.EncodeToString(hmacSHA256([]byte(auth.Secret), "ACS3-HMAC-SHA256\n"+sha256Hex([]byte(request))))
	return "ACS3-HMAC-SHA256 Credential=" + auth.ID + ",SignedHeaders=" + signed + ",Signature=" + signature
}
