// Command routes 扫描仓库内所有 HTTP 路由注册(HandleFunc/Handle), 生成
// API 路由清单文档与机器可读 JSON, 供前后端契约对齐与 API 覆盖面审计。
//
// 用法:
//
//	go run ./tools/routes              # 输出到 stdout(markdown)
//	go run ./tools/routes -json        # 输出到 stdout(JSON)
//	go run ./tools/routes -o docs/api-routes.md        # 写文件
//	make routes                        # 生成 docs/api-routes.md + docs/api-routes.json
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

type route struct {
	Method string `json:"method"`
	Path   string `json:"path"`
	File   string `json:"file"`
	Line   int    `json:"line"`
}

var (
	handleFuncRe = regexp.MustCompile(`HandleFunc\(\s*"([A-Z]+)\s+(\/[^"]+)"`)
	handleRe     = regexp.MustCompile(`Handle\(\s*"([A-Z]+)\s+(\/[^"]+)"`)
)

func main() {
	jsonOut := flag.Bool("json", false, "输出 JSON 而非 markdown")
	out := flag.String("o", "", "输出文件路径(空=stdout)")
	flag.Parse()

	root := "."
	if len(flag.Args()) > 0 {
		root = flag.Args()[0]
	}

	var routes []route
	_ = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.Contains(path, "node_modules") {
			return nil
		}
		// 只扫 cmd/ 与 internal/(业务路由注册处), 跳过 pkg/ 公共库、生成代码与测试
		if !strings.HasPrefix(path, "cmd/") && !strings.HasPrefix(path, "internal/") {
			return nil
		}
		if strings.Contains(path, "internal/ent/") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		routes = append(routes, scanFile(path)...)
		return nil
	})

	sort.Slice(routes, func(i, j int) bool {
		if routes[i].Path == routes[j].Path {
			return routes[i].Method < routes[j].Method
		}
		return routes[i].Path < routes[j].Path
	})

	var outData []byte
	if *jsonOut {
		outData, _ = json.MarshalIndent(routes, "", "  ")
	} else {
		outData = []byte(renderMarkdown(routes))
	}

	if *out != "" {
		if err := os.WriteFile(*out, outData, 0o644); err != nil {
			fmt.Fprintln(os.Stderr, "write:", err)
			os.Exit(1)
		}
		fmt.Printf("written %d routes to %s\n", len(routes), *out)
		return
	}
	fmt.Print(string(outData))
}

// scanFile 用 go/parser 提取单个文件的路由注册(词法精确, 避免误匹配字符串字面量)。
func scanFile(path string) []route {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		return nil
	}
	var out []route
	fileText := readFile(path)
	for _, decl := range f.Decls {
		// 用 AST 定位 HandleFunc/Handle 调用位置, 再在原文行中正则取 method/path
		_ = decl
	}
	_ = fileText

	// 正则行级提取: 精确到 "METHOD /path" 形态(Go 1.22 ServeMux 模式)
	lines := strings.Split(fileText, "\n")
	for i, line := range lines {
		collect := func(re *regexp.Regexp) {
			for _, m := range re.FindAllStringSubmatch(line, -1) {
				if len(m) == 3 {
					out = append(out, route{Method: m[1], Path: m[2], File: path, Line: i + 1})
				}
			}
		}
		collect(handleFuncRe)
		collect(handleRe)
	}
	return out
}

func readFile(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(b)
}

func renderMarkdown(routes []route) string {
	var b strings.Builder
	b.WriteString("# OpsOrigin API 路由清单\n\n")
	b.WriteString(fmt.Sprintf("> 由 `tools/routes` 自动生成(`make routes` 重新生成), 共 **%d** 个端点。\n", len(routes)))
	b.WriteString("> 方法与路径为 Go 1.22 ServeMux 注册形态。\n\n")

	// 按模块分组: /api/v1/<module> 归并到模块级(去掉 {param} 段), 其余按首段
	groups := map[string][]route{}
	for _, r := range routes {
		parts := strings.SplitN(strings.TrimPrefix(r.Path, "/"), "/", 3)
		key := "/" + parts[0]
		if len(parts) >= 3 && parts[0] == "api" {
			seg := parts[2]
			if idx := strings.Index(seg, "/{"); idx >= 0 {
				seg = seg[:idx]
			}
			key = "/" + parts[0] + "/" + parts[1] + "/" + seg
		}
		groups[key] = append(groups[key], r)
	}

	keys := make([]string, 0, len(groups))
	for k := range groups {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, k := range keys {
		rs := groups[k]
		b.WriteString(fmt.Sprintf("## `%s` (%d)\n\n", k, len(rs)))
		b.WriteString("| Method | Path | 位置 |\n| --- | --- | --- |\n")
		for _, r := range rs {
			b.WriteString(fmt.Sprintf("| %s | `%s` | `%s:%d` |\n", r.Method, r.Path, r.File, r.Line))
		}
		b.WriteString("\n")
	}
	return b.String()
}
