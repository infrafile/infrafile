package plugin

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
)

func (b *builtin) tencent(ctx context.Context, config map[string]any) (any, error) {
	auth, services, err := b.cloudConfig(ctx, config, []string{"domains", "cdn"})
	if err != nil {
		return nil, err
	}
	result := map[string]any{}
	for _, service := range services {
		domains, cdn := []domainEntry{}, []cdnEntry{}
		for offset := 0; ; {
			var response struct {
				Response struct {
					Error *struct {
						Code string `json:"Code"`
					} `json:"Error"`
					TotalCount  *int `json:"TotalCount"`
					TotalNumber *int `json:"TotalNumber"`
					DomainSet   []struct {
						Name      string `json:"DomainName"`
						ID        string `json:"DomainId"`
						ExpiresAt string `json:"ExpirationDate"`
						Status    string `json:"BuyStatus"`
					} `json:"DomainSet"`
					Domains []struct {
						tencentOriginDetail
						Name   string `json:"Domain"`
						ID     string `json:"ResourceId"`
						CNAME  string `json:"Cname"`
						Status string `json:"Status"`
						Type   string `json:"ServiceType"`
						Area   string `json:"Area"`
					} `json:"Domains"`
				} `json:"Response"`
			}
			api, action, version := "domain", "DescribeDomainNameList", "2018-08-08"
			if service == "cdn" {
				api, action, version = "cdn", "DescribeDomains", "2018-06-06"
				if config["collect_origin"] == true {
					action = "DescribeDomainsConfig"
				}
			}
			body, _ := json.Marshal(map[string]int{"Offset": offset, "Limit": 100})
			if err := tencentRequest(ctx, api, action, version, body, auth, &response); err != nil {
				return nil, err
			}
			r := response.Response
			if r.Error != nil {
				return nil, failure("provider", "腾讯云 API 拒绝请求")
			}
			total, count := r.TotalCount, len(r.DomainSet)
			if service == "cdn" {
				total, count = r.TotalNumber, len(r.Domains)
			}
			if total == nil || *total < 0 || (service == "domains" && r.DomainSet == nil) || (service == "cdn" && r.Domains == nil) {
				return nil, failure("protocol", "腾讯云分页响应不完整")
			}
			if service == "cdn" && config["collect_origin"] == true && (*total > maxOriginDomains || offset+count > maxOriginDomains) {
				return nil, failure("config", "回源采集超过 200 个域名上限")
			}
			for _, item := range r.DomainSet {
				if item.Name == "" {
					return nil, failure("protocol", "腾讯云域名缺少名称")
				}
				date, err := dateOnly(item.ExpiresAt)
				if err != nil {
					return nil, err
				}
				domains = append(domains, domainEntry{Name: item.Name, ID: item.ID, Status: item.Status, ExpiresAt: date})
			}
			for _, item := range r.Domains {
				if item.Name == "" {
					return nil, failure("protocol", "腾讯云 CDN 缺少名称")
				}
				entry := cdnEntry{Name: item.Name, ID: item.ID, CNAME: item.CNAME, Status: item.Status, Type: item.Type, Area: item.Area}
				if config["collect_origin"] == true {
					entry.Origin = item.tencentOriginDetail.observation()
				}
				cdn = append(cdn, entry)
			}
			offset += count
			if offset >= *total {
				break
			}
			if count == 0 {
				return nil, failure("protocol", "腾讯云分页提前结束")
			}
		}
		sort.Slice(domains, func(i, j int) bool { return domains[i].Name < domains[j].Name })
		sort.Slice(cdn, func(i, j int) bool { return cdn[i].Name < cdn[j].Name })
		if service == "domains" {
			result[service] = domains
		} else {
			result[service] = cdn
		}
	}
	return result, nil
}

func tencentRequest(ctx context.Context, service, action, version string, body []byte, auth cloudAuth, result any) error {
	host := service + ".tencentcloudapi.com"
	headers := http.Header{}
	headers.Set("Host", host)
	headers.Set("Content-Type", "application/json; charset=utf-8")
	headers.Set("X-Tc-Action", action)
	headers.Set("X-Tc-Version", version)
	now := time.Now().UTC()
	headers.Set("X-Tc-Timestamp", strconv.FormatInt(now.Unix(), 10))
	if auth.Token != "" {
		headers.Set("X-Tc-Token", auth.Token)
	}
	headers.Set("Authorization", tc3Authorization(headers, service, now, body, auth))
	return requestJSON(ctx, "POST", "https://"+host+"/", headers, body, result)
}

func tc3Authorization(headers http.Header, service string, now time.Time, body []byte, auth cloudAuth) string {
	const signed = "content-type;host;x-tc-action"
	canonical := ""
	for _, key := range strings.Split(signed, ";") {
		canonical += key + ":" + strings.ToLower(strings.TrimSpace(headers.Get(key))) + "\n"
	}
	request := "POST\n/\n\n" + canonical + "\n" + signed + "\n" + sha256Hex(body)
	date := now.UTC().Format("2006-01-02")
	scope := date + "/" + service + "/tc3_request"
	toSign := "TC3-HMAC-SHA256\n" + strconv.FormatInt(now.Unix(), 10) + "\n" + scope + "\n" + sha256Hex([]byte(request))
	key := hmacSHA256([]byte("TC3"+auth.Secret), date)
	key = hmacSHA256(key, service)
	key = hmacSHA256(key, "tc3_request")
	signature := hex.EncodeToString(hmacSHA256(key, toSign))
	return "TC3-HMAC-SHA256 Credential=" + auth.ID + "/" + scope + ", SignedHeaders=" + signed + ", Signature=" + signature
}
