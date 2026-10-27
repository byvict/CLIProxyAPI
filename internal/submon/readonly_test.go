package submon

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strconv"
	"testing"
)

func TestNativeReadersHaveNoCredentialWriteOrInferenceCapability(t *testing.T) {
	allowed := map[string]bool{"https://chatgpt.com/backend-api/wham/usage": true, "https://cli-chat-proxy.grok.com/v1/billing?format=credits": true, "https://api.anthropic.com/api/oauth/profile": true, "https://api.anthropic.com/api/oauth/usage": true, "https://api.meta.ai/muse-code/key": true}
	for _, file := range []string{"native.go", "sources.go"} {
		tree, err := parser.ParseFile(token.NewFileSet(), file, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		var requestBody *ast.BlockStmt
		for _, decl := range tree.Decls {
			if fn, valid := decl.(*ast.FuncDecl); valid && file == "sources.go" && fn.Name.Name == "request" {
				requestBody = fn.Body
			}
		}
		ast.Inspect(tree, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			if sel.Sel.Name == "Do" && (requestBody == nil || call.Pos() < requestBody.Pos() || call.End() > requestBody.End()) {
				t.Error("network call bypasses quota request boundary")
			}
			pkg, ok := sel.X.(*ast.Ident)
			if !ok {
				if sel.Sel.Name == "Do" {
					x, isClient := sel.X.(*ast.SelectorExpr)
					if !isClient || x.Sel.Name != "client" || file != "sources.go" {
						t.Error("network call bypasses quota transport")
					}
				}
				return true
			}
			if pkg.Name == "os" {
				switch sel.Sel.Name {
				case "WriteFile", "OpenFile", "Create", "Rename", "Remove", "RemoveAll", "Mkdir", "MkdirAll":
					t.Errorf("native reader can write filesystem through %s", sel.Sel.Name)
				}
			}
			if pkg.Name == "exec" {
				if sel.Sel.Name != "CommandContext" || len(call.Args) != 3 {
					t.Error("unrecognized native credential command")
					return true
				}
				executable, ok := call.Args[1].(*ast.BasicLit)
				if !ok || executable.Value != `"/usr/bin/security"` {
					t.Error("native credential command is not security")
				}
			}
			if pkg.Name == "http" && (sel.Sel.Name == "Get" || sel.Sel.Name == "Post" || sel.Sel.Name == "Do") {
				t.Error("network call bypasses quota transport")
			}
			if sel.Sel.Name == "request" {
				if len(call.Args) != 6 {
					t.Error("unrecognized quota call")
					return true
				}
				literal, ok := call.Args[2].(*ast.BasicLit)
				if !ok {
					t.Error("dynamic provider endpoint")
					return true
				}
				url, err := strconv.Unquote(literal.Value)
				if err != nil || !allowed[url] {
					t.Error("non-quota provider endpoint")
				}
			}
			return true
		})
		if file == "native.go" {
			ast.Inspect(tree, func(n ast.Node) bool {
				literal, ok := n.(*ast.BasicLit)
				if !ok || literal.Kind != token.STRING {
					return true
				}
				value, _ := strconv.Unquote(literal.Value)
				for _, writeCommand := range []string{"add-generic-password", "delete-generic-password", "set-generic-password-partition-list"} {
					if value == writeCommand {
						t.Error("Keychain write command")
					}
				}
				return true
			})
		}
	}
}
