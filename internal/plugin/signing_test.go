package plugin

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

// 固定字节来自官方公开向量，来源和独立复算见 docs/research/。
func TestACS3OfficialVector(t *testing.T) {
	headers := http.Header{}
	for key, value := range map[string]string{"host": "ecs.cn-shanghai.aliyuncs.com", "x-acs-action": "RunInstances", "x-acs-version": "2014-05-26", "x-acs-date": "2023-10-26T10:22:32Z", "x-acs-signature-nonce": "3156853299f313e23d1673dc12e1703d", "x-acs-content-sha256": "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"} {
		headers.Set(key, value)
	}
	auth := acs3Authorization(headers, "ImageId=win2019_1809_x64_dtc_zh-cn_40G_alibase_20230811.vhd&RegionId=cn-shanghai", cloudAuth{ID: "YourAccessKeyId", Secret: "YourAccessKeySecret"})
	if !strings.HasSuffix(auth, "Signature=06563a9e1b43f5dfe96b81484da74bceab24a1d853912eee15083a6f0f3283c0") {
		t.Fatalf("ACS3 official vector: %s", auth)
	}
}

func TestTC3ReferenceVector(t *testing.T) {
	headers := http.Header{}
	headers.Set("Host", "cvm.tencentcloudapi.com")
	headers.Set("Content-Type", "application/json; charset=utf-8")
	headers.Set("X-Tc-Action", "DescribeInstances")
	body := []byte(`{"Limit": 1, "Filters": [{"Values": ["\u672a\u547d\u540d"], "Name": "instance-name"}]}`)
	if sha256Hex(body) != "35e9c5b0e3ae67532d3c9f17ead6c90222632e5b1ff7f6e89887f1398934f064" {
		t.Fatal("reference body changed")
	}
	auth := tc3Authorization(headers, "cvm", time.Unix(1551113065, 0), body, cloudAuth{ID: "testid", Secret: "testsecret"})
	if !strings.HasSuffix(auth, "Signature=5783a974073f79ab46334c07e383b65090ab676d96e986a76dab65af9957b106") {
		t.Fatalf("TC3 reference vector: %s", auth)
	}
}

func TestBCEOfficialVector(t *testing.T) {
	headers := http.Header{}
	for key, value := range map[string]string{"host": "bj.bcebos.com", "content-length": "8", "content-type": "text/plain", "content-md5": "NFzcPqhviddjRNnSOGo4rw==", "x-bce-date": "2015-04-27T08:23:49Z"} {
		headers.Set(key, value)
	}
	auth := bceAuthorization("PUT", "/v1/test/myfolder/readme.txt", "partNumber=9&uploadId=a44cc9bab11cbd156984767aad637851", headers, cloudAuth{ID: strings.Repeat("a", 32), Secret: strings.Repeat("b", 32)})
	if !strings.HasSuffix(auth, "/d74a04362e6a848f5b39b15421cb449427f419c95a480fd6b8cf9fc783e2999e") {
		t.Fatalf("BCE official vector: %s", auth)
	}
}

func TestEOPReferenceVector(t *testing.T) {
	auth := eopAuthorization("00000000-0000-4000-8000-000000000001", "20220525T160752Z", "area_scope=1&page=1&page_size=100", cloudAuth{ID: strings.Repeat("a", 32), Secret: strings.Repeat("b", 32)})
	if !strings.HasSuffix(auth, "Signature=wHFhvGz6dxDK5GTV35/RLO6L2WNEXsOvxb7njKai5r0=") {
		t.Fatalf("EOP reference vector: %s", auth)
	}
}
