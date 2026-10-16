// Copyright 2019-present Facebook
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package entgql

import (
	"bytes"
	"go/ast"
	"go/parser"
	"go/token"
	"strconv"
	"testing"
	"text/template"

	"entgo.io/ent/entc/gen"
	"entgo.io/ent/entc/load"
	"entgo.io/ent/schema/field"
	"github.com/stretchr/testify/require"
)

func TestCollectionIDCollectedFor(t *testing.T) {
	for _, tt := range []struct {
		name   string
		fields []*field.Descriptor
		cases  map[string][]string
	}{
		{
			name: "regular field with default ID",
			fields: []*field.Descriptor{
				field.String("name").Annotations(CollectedFor("uppercaseName")).Descriptor(),
			},
			cases: map[string][]string{"id": nil, "uppercaseName": {"FieldName"}},
		},
		{
			name: "ID dependency",
			fields: []*field.Descriptor{
				field.Int("id").Annotations(CollectedFor("isViewer", "viewerCanFollow")).Descriptor(),
				field.String("name").Descriptor(),
			},
			cases: map[string][]string{"id": nil, "isViewer": nil, "viewerCanFollow": nil, "name": {"FieldName"}},
		},
		{
			name: "ID and scalar dependency",
			fields: []*field.Descriptor{
				field.Int("id").Annotations(CollectedFor("label")).Descriptor(),
				field.String("name").Annotations(CollectedFor("label")).Descriptor(),
			},
			cases: map[string][]string{"id": nil, "label": {"FieldName"}},
		},
		{
			name: "ID-only schema",
			fields: []*field.Descriptor{
				field.Int("id").Annotations(CollectedFor("isViewer")).Descriptor(),
			},
			cases: map[string][]string{"id": nil, "isViewer": nil},
		},
		{
			name: "mapped string ID",
			fields: []*field.Descriptor{
				field.String("id").Annotations(MapsTo("identifier"), CollectedFor("isViewer")).Descriptor(),
				field.String("name").Descriptor(),
			},
			cases: map[string][]string{"identifier": nil, "isViewer": nil},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var fields []*load.Field
			for _, descriptor := range tt.fields {
				f, err := load.NewField(descriptor)
				require.NoError(t, err)
				fields = append(fields, f)
			}
			graph, err := gen.NewGraph(&gen.Config{Package: "example.com/ent"}, &load.Schema{Name: "User", Fields: fields})
			require.NoError(t, err)
			tmpl := gen.NewTemplate("collection-test").Funcs(TemplateFuncs)
			// Bind availability checks to this isolated template set.
			tmpl.Funcs(template.FuncMap{"hasTemplate": func(name string) bool { return tmpl.Lookup(name) != nil }})
			for _, source := range CollectionTemplate.Templates() {
				if source.Tree != nil {
					_, err := tmpl.AddParseTree(source.Name(), source.Tree)
					require.NoError(t, err)
				}
			}
			_, err = tmpl.Parse(`{{define "header"}}package ent{{end}}{{define "import"}}{{end}}`)
			require.NoError(t, err)
			var output bytes.Buffer
			require.NoError(t, tmpl.ExecuteTemplate(&output, "gql_collection", graph))
			parsed, err := parser.ParseFile(token.NewFileSet(), "gql_collection.go", output.Bytes(), 0)
			require.NoError(t, err)
			cases := make(map[string]*ast.CaseClause)
			for _, decl := range parsed.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok || fn.Name.Name != "collectField" {
					continue
				}
				ast.Inspect(fn.Body, func(node ast.Node) bool {
					clause, ok := node.(*ast.CaseClause)
					if !ok {
						return true
					}
					for _, expr := range clause.List {
						literal, ok := expr.(*ast.BasicLit)
						if !ok || literal.Kind != token.STRING {
							continue
						}
						name, err := strconv.Unquote(literal.Value)
						require.NoError(t, err)
						require.NotContains(t, cases, name, "duplicate collector case")
						cases[name] = clause
					}
					return true
				})
			}
			for name, want := range tt.cases {
				clause, ok := cases[name]
				require.True(t, ok, "missing collector case for %q", name)
				var columns []string
				for _, stmt := range clause.Body {
					ast.Inspect(stmt, func(node ast.Node) bool {
						call, ok := node.(*ast.CallExpr)
						if !ok || len(call.Args) != 2 {
							return true
						}
						fn, ok := call.Fun.(*ast.Ident)
						if !ok || fn.Name != "append" {
							return true
						}
						if column, ok := call.Args[1].(*ast.SelectorExpr); ok {
							columns = append(columns, column.Sel.Name)
						}
						return true
					})
				}
				require.ElementsMatch(t, want, columns, "columns appended for %q", name)
			}
			require.Contains(t, output.String(), "unknownSeen = true", "unknown-field fallback must remain")
		})
	}
}
