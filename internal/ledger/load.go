package ledger

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"
)

// Document 是解析后的一个 YAML 文件，记住了每个字段所在的行号。
type Document struct {
	File  string // 相对数据仓根目录、以 / 分隔的路径
	Value any    // map[string]any、[]any、string、整数、浮点数、bool 或 nil
	lines map[string]int
}

// Line 返回字段路径所在的行号，找不到时返回 0。路径 "" 表示整个文件。
func (d *Document) Line(path string) int {
	return d.lines[path]
}

// Record 是 inventory/ 下的一条记录。
type Record struct {
	*Document
	Kind string // 由所在目录决定
	Name string // metadata.name；缺失或不是字符串时为空
}

// Stem 返回不带目录和扩展名的文件名，按约定应等于 metadata.name。
func (r *Record) Stem() string {
	return strings.TrimSuffix(filepath.Base(r.File), ".yaml")
}

// Archived 报告记录是否带有 metadata.archived。
func (r *Record) Archived() bool {
	md, _ := field(r.Value, "metadata").(map[string]any)
	_, ok := md["archived"]
	return ok
}

// Repo 是加载后的数据仓。
type Repo struct {
	Dir     string
	Config  *Document // infrafile.yaml；无法解析时为 nil
	Records []*Record // 无法解析或 kind 与目录不符的文件不在其中
	Files   []string  // inventory/<kind>/ 下的全部 .yaml 文件，包括有问题的
}

// NotRepoError 表示目录里没有 infrafile.yaml。
type NotRepoError struct {
	Dir string
}

func (e *NotRepoError) Error() string {
	return fmt.Sprintf("%s 不是数据仓：缺少 %s（可以用 infrafile init 创建）", e.Dir, ConfigFile)
}

// Load 加载数据仓。数据本身的问题以 Problem 返回；
// 不是数据仓、目录读不了等无法继续的情况才返回 error。
func Load(dir string) (*Repo, []Problem, error) {
	if _, err := os.Stat(filepath.Join(dir, ConfigFile)); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil, &NotRepoError{Dir: dir}
		}
		return nil, nil, err
	}

	repo := &Repo{Dir: dir}
	cfg, problems := parseFile(dir, ConfigFile)
	repo.Config = cfg

	entries, err := os.ReadDir(filepath.Join(dir, InventoryDir))
	if errors.Is(err, fs.ErrNotExist) {
		return repo, problems, nil
	}
	if err != nil {
		return nil, nil, err
	}
	for _, e := range entries {
		name := e.Name()
		rel := InventoryDir + "/" + name
		if strings.HasPrefix(name, ".") {
			continue
		}
		if !e.IsDir() {
			if isYAML(name) {
				problems = append(problems, Problem{Code: CodeLayout, File: rel,
					Message: "记录文件要放在 kind 目录下，如 inventory/servers/"})
			}
			continue
		}
		k, ok := KindByDir(name)
		if !ok {
			problems = append(problems, Problem{Code: CodeLayout, File: rel + "/",
				Message: "未知目录，inventory/ 下只能有 " + kindDirs()})
			continue
		}
		ps, err := repo.loadKind(k)
		if err != nil {
			return nil, nil, err
		}
		problems = append(problems, ps...)
	}
	return repo, problems, nil
}

func (repo *Repo) loadKind(k Kind) ([]Problem, error) {
	entries, err := os.ReadDir(filepath.Join(repo.Dir, InventoryDir, k.Dir))
	if err != nil {
		return nil, err
	}
	var problems []Problem
	for _, e := range entries {
		name := e.Name()
		rel := InventoryDir + "/" + k.Dir + "/" + name
		switch {
		case strings.HasPrefix(name, "."):
		case e.IsDir():
			problems = append(problems, Problem{Code: CodeLayout, File: rel + "/",
				Message: "kind 目录下不能有子目录"})
		case strings.HasSuffix(name, ".yml"):
			problems = append(problems, Problem{Code: CodeLayout, File: rel,
				Message: "扩展名要用 .yaml"})
		case strings.HasSuffix(name, ".yaml"):
			repo.Files = append(repo.Files, rel)
			doc, ps := parseFile(repo.Dir, rel)
			problems = append(problems, ps...)
			if doc == nil {
				continue
			}
			if kind, ok := field(doc.Value, "kind").(string); ok && kind != k.Name {
				problems = append(problems, Problem{Code: CodeKind, File: rel, Line: doc.Line("kind"), Path: "kind",
					Message: fmt.Sprintf("kind 是 %s，但 %s/ 目录下只放 %s", kind, k.Dir, k.Name)})
				continue
			}
			md, _ := field(doc.Value, "metadata").(map[string]any)
			name, _ := md["name"].(string)
			repo.Records = append(repo.Records, &Record{Document: doc, Kind: k.Name, Name: name})
		}
	}
	return problems, nil
}

func kindDirs() string {
	dirs := make([]string, len(Kinds))
	for i, k := range Kinds {
		dirs[i] = k.Dir + "/"
	}
	return strings.Join(dirs, "、")
}

func isYAML(name string) bool {
	return strings.HasSuffix(name, ".yaml") || strings.HasSuffix(name, ".yml")
}

func field(v any, key string) any {
	m, _ := v.(map[string]any)
	return m[key]
}

var yamlErrLine = regexp.MustCompile(`^yaml: line (\d+): (.*)$`)

// parseFile 解析一个文件。返回的 Document 为 nil 表示文件无法作为一条记录使用。
func parseFile(dir, rel string) (*Document, []Problem) {
	data, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel)))
	if err != nil {
		return nil, []Problem{{Code: CodeParse, File: rel, Message: "读取失败：" + err.Error()}}
	}
	return ParseDocument(rel, data)
}

// ParseDocument 复用台账的 YAML 语义：拒绝重复键、多文档，并保留日期字符串。
func ParseDocument(rel string, data []byte) (*Document, []Problem) {
	dec := yaml.NewDecoder(bytes.NewReader(data))
	var root yaml.Node
	if err := dec.Decode(&root); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, []Problem{{Code: CodeParse, File: rel, Message: "文件是空的"}}
		}
		p := Problem{Code: CodeParse, File: rel, Message: "YAML 语法错误：" + err.Error()}
		if m := yamlErrLine.FindStringSubmatch(err.Error()); m != nil {
			p.Line, _ = strconv.Atoi(m[1])
			p.Message = "YAML 语法错误：" + m[2]
		}
		return nil, []Problem{p}
	}
	var extra yaml.Node
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		return nil, []Problem{{Code: CodeParse, File: rel, Line: extra.Line,
			Message: "一个文件只能放一条记录，这里有多个 YAML 文档"}}
	}

	c := &converter{file: rel, lines: map[string]int{"": 1}}
	v := c.value(&root, "", 0)
	return &Document{File: rel, Value: v, lines: c.lines}, c.problems
}

// converter 把 yaml.Node 转成普通的 Go 值，同时记录每个字段路径的行号。
// 不直接 Unmarshal 到 any，是为了拿到行号、拦下重复键，并让日期保持字符串原样交给 schema 校验。
type converter struct {
	file     string
	lines    map[string]int
	problems []Problem
}

const maxDepth = 100

func (c *converter) problem(line int, path, msg string) {
	c.problems = append(c.problems, Problem{Code: CodeParse, File: c.file, Line: line, Path: path, Message: msg})
}

func (c *converter) value(n *yaml.Node, path string, depth int) any {
	if depth > maxDepth {
		c.problem(n.Line, path, "嵌套太深，可能是别名引用了自身")
		return nil
	}
	switch n.Kind {
	case yaml.DocumentNode:
		if len(n.Content) == 0 {
			return nil
		}
		return c.value(n.Content[0], path, depth+1)
	case yaml.AliasNode:
		return c.value(n.Alias, path, depth+1)
	case yaml.MappingNode:
		m := make(map[string]any, len(n.Content)/2)
		for i := 0; i+1 < len(n.Content); i += 2 {
			k, v := n.Content[i], n.Content[i+1]
			if k.Kind == yaml.AliasNode {
				k = k.Alias
			}
			if k.Kind != yaml.ScalarNode {
				c.problem(k.Line, path, "键必须是字符串")
				continue
			}
			if k.ShortTag() == "!!merge" {
				c.problem(k.Line, path, "不支持合并键 <<")
				continue
			}
			p := JoinKey(path, k.Value)
			if _, dup := m[k.Value]; dup {
				c.problem(k.Line, p, "字段重复")
				continue
			}
			c.lines[p] = k.Line
			m[k.Value] = c.value(v, p, depth+1)
		}
		return m
	case yaml.SequenceNode:
		s := make([]any, 0, len(n.Content))
		for i, item := range n.Content {
			p := JoinIndex(path, i)
			c.lines[p] = item.Line
			s = append(s, c.value(item, p, depth+1))
		}
		return s
	case yaml.ScalarNode:
		switch n.ShortTag() {
		case "!!int", "!!float", "!!bool", "!!null":
			var v any
			if err := n.Decode(&v); err == nil {
				return v
			}
		}
		// 字符串、日期（!!timestamp）和自定义标签都保留原文。
		return n.Value
	}
	return nil
}
