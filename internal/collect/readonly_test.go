package collect

import (
	"go/ast"
	"go/parser"
	"go/token"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/costexplorer"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/elasticache"
	"github.com/aws/aws-sdk-go-v2/service/elasticloadbalancing"
	"github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2"
	"github.com/aws/aws-sdk-go-v2/service/kafka"
	"github.com/aws/aws-sdk-go-v2/service/rds"
)

var roTestReadPrefixes = []string{"Describe", "List", "Get"}

func roTestIsReadOnlyName(name string) bool {
	return slices.ContainsFunc(roTestReadPrefixes, func(p string) bool { return strings.HasPrefix(name, p) })
}

// roTestInterfaces is every read-only interface: each AWSClients field plus the Cost Explorer client.
func roTestInterfaces(t *testing.T) map[string]reflect.Type {
	t.Helper()
	out := map[string]reflect.Type{"CostExplorerReadOnly": reflect.TypeFor[CostExplorerReadOnly]()}
	for f := range reflect.TypeFor[AWSClients]().Fields() {
		out["AWSClients."+f.Name] = f.Type
	}
	return out
}

func TestReadOnlyInterfacesDeclareOnlyDescribeListGetMethods(t *testing.T) {
	for name, typ := range roTestInterfaces(t) {
		t.Run(name, func(t *testing.T) {
			// Arrange / Act
			isInterface := typ.Kind() == reflect.Interface

			// Assert
			if !isInterface {
				t.Fatalf("%s is %s, want an interface (a concrete SDK client would expose mutating calls)", name, typ.Kind())
			}
			if typ.NumMethod() == 0 {
				t.Fatalf("%s declares no methods", name)
			}
			for m := range typ.Methods() {
				if !roTestIsReadOnlyName(m.Name) {
					t.Errorf("%s.%s is not a Describe*/List*/Get* call", name, m.Name)
				}
			}
		})
	}
}

// roTestMutatingSDKMethods is every method of the real SDK clients that is not read-only.
func roTestMutatingSDKMethods() map[string]bool {
	clients := []reflect.Type{
		reflect.TypeFor[*ec2.Client](),
		reflect.TypeFor[*elasticloadbalancingv2.Client](),
		reflect.TypeFor[*elasticloadbalancing.Client](),
		reflect.TypeFor[*rds.Client](),
		reflect.TypeFor[*elasticache.Client](),
		reflect.TypeFor[*kafka.Client](),
		reflect.TypeFor[*costexplorer.Client](),
	}
	out := map[string]bool{}
	for _, c := range clients {
		for m := range c.Methods() {
			if !roTestIsReadOnlyName(m.Name) {
				out[m.Name] = true
			}
		}
	}
	return out
}

// roTestForbiddenSelectors returns every selector (call or method value) whose name is a mutating SDK client method.
// Name-based on purpose: it needs no type checking, so it also catches calls on clients built inline.
func roTestForbiddenSelectors(t *testing.T, filename string, src any, mutating map[string]bool) []string {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, filename, src, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parse %s: %v", filename, err)
	}
	var found []string
	ast.Inspect(f, func(n ast.Node) bool {
		if sel, ok := n.(*ast.SelectorExpr); ok && mutating[sel.Sel.Name] {
			found = append(found, fset.Position(sel.Pos()).String()+" "+sel.Sel.Name)
		}
		return true
	})
	return found
}

func TestAWSSourcesReferenceNoMutatingSDKMethods(t *testing.T) {
	mutating := roTestMutatingSDKMethods()
	if !mutating["TerminateInstances"] || !mutating["DeleteLoadBalancer"] {
		t.Fatal("reflection over SDK clients did not find known mutating methods")
	}
	for _, file := range []string{"aws.go", "ce.go"} {
		t.Run(file, func(t *testing.T) {
			// Act
			found := roTestForbiddenSelectors(t, file, nil, mutating)

			// Assert
			if len(found) > 0 {
				t.Errorf("mutating SDK methods referenced:\n%s", strings.Join(found, "\n"))
			}
		})
	}
}

func TestForbiddenSelectorCheckDetectsMutatingCalls(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want int
	}{
		{
			name: "call on an inline client",
			src:  "package x\nfunc f() { ec2.NewFromConfig(cfg).TerminateInstances(ctx, nil) }",
			want: 1,
		},
		{
			name: "method value",
			src:  "package x\nfunc f(c *ec2.Client) { g := c.CreateTags; _ = g }",
			want: 1,
		},
		{
			name: "read-only calls pass",
			src:  "package x\nfunc f(c EC2ReadOnly) { c.DescribeInstances(ctx, nil); p.NextPage(ctx) }",
			want: 0,
		},
	}
	mutating := roTestMutatingSDKMethods()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Act
			found := roTestForbiddenSelectors(t, "snippet.go", tt.src, mutating)

			// Assert
			if len(found) != tt.want {
				t.Errorf("found %v, want %d findings", found, tt.want)
			}
		})
	}
}
