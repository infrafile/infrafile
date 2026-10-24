// Package ledger 负责数据仓的布局、记录加载和内嵌的 JSON Schema。
package ledger

// APIVersion 是当前记录格式的版本。
const APIVersion = "infrafile/v1alpha1"

// ConfigFile 是数据仓根目录下的配置文件名，也是识别数据仓的标志。
const ConfigFile = "infrafile.yaml"

// InventoryDir 存放手写的台账记录。
const InventoryDir = "inventory"

// Kind 描述一种记录类型。
type Kind struct {
	Name string // 如 Server
	Dir  string // inventory/ 下的目录名，即 kind 的复数小写
}

// Kinds 按设计稿的顺序列出全部记录类型。
var Kinds = []Kind{
	{Name: "Server", Dir: "servers"},
	{Name: "App", Dir: "apps"},
	{Name: "Domain", Dir: "domains"},
	{Name: "Account", Dir: "accounts"},
	{Name: "Resource", Dir: "resources"},
	{Name: "Repo", Dir: "repos"},
}

// KindByName 按 kind 名查找，找不到时返回 false。
func KindByName(name string) (Kind, bool) {
	for _, k := range Kinds {
		if k.Name == name {
			return k, true
		}
	}
	return Kind{}, false
}

// KindByDir 按目录名查找，找不到时返回 false。
func KindByDir(dir string) (Kind, bool) {
	for _, k := range Kinds {
		if k.Dir == dir {
			return k, true
		}
	}
	return Kind{}, false
}
