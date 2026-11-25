package benchrun

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"gofl/internal/contract"
	"gofl/internal/proc"
)

// Target is the package that holds the benchmark.
type Target struct {
	ImportPath string
	Dir        string
	// HasSub: the benchmark calls b.Run, so each size is a sub-benchmark
	// selected by name. Otherwise the benchmark reads its size from the
	// GOFL_SIZE environment variable.
	HasSub bool
	// ReadsSize: some test file of the package mentions "GOFL_SIZE".
	ReadsSize bool
}

// listed is the part of `go list -json` gofl reads.
type listed struct {
	ImportPath   string
	Dir          string
	TestGoFiles  []string
	XTestGoFiles []string
	Error        *struct{ Err string }
}

func (p *listed) testFiles() []string {
	var out []string
	for _, f := range append(append([]string{}, p.TestGoFiles...), p.XTestGoFiles...) {
		out = append(out, filepath.Join(p.Dir, f))
	}
	return out
}

// list runs `go list -e -json pattern` in the project directory.
func (s *Session) list(ctx context.Context, pattern string) ([]*listed, error) {
	var stdout, stderr bytes.Buffer
	res := proc.Run(ctx, proc.Spec{
		Name:    s.Go.Path,
		Args:    []string{"list", "-e", "-json", pattern},
		Dir:     s.o.Dir,
		Env:     s.env(),
		Timeout: s.buildTimeout(),
		Out:     &stdout,
		Err:     &stderr,
	})
	switch {
	case res.Err != nil:
		return nil, res.Err
	case res.TimedOut:
		return nil, &Failure{Code: contract.Timeout, Msg: fmt.Sprintf("go list did not finish within %s", s.buildTimeout())}
	case res.ExitCode != 0:
		text := stderr.String()
		return nil, &Failure{Code: classifyBuild(text), Msg: "go list: " + firstError(text)}
	}
	var pkgs []*listed
	dec := json.NewDecoder(&stdout)
	for {
		var p listed
		if err := dec.Decode(&p); err == io.EOF {
			break
		} else if err != nil {
			return nil, fmt.Errorf("go list: unexpected output: %v", err)
		}
		pkgs = append(pkgs, &p)
	}
	return pkgs, nil
}

// Find locates the package that declares the benchmark: the configured
// package, or the single package under the project directory that has it.
func (s *Session) Find(ctx context.Context) (*Target, error) {
	pattern := "./..."
	if s.o.Package != "" {
		pattern = s.o.Package
	}
	pkgs, err := s.list(ctx, pattern)
	if err != nil {
		return nil, err
	}
	var found []*Target
	for _, p := range pkgs {
		if p.Error != nil && s.o.Package != "" {
			return nil, &Failure{Code: classifyBuild(p.Error.Err), Msg: "package " + s.o.Package + ": " + firstError(p.Error.Err)}
		}
		decl := scanPackage(p.testFiles(), s.o.Bench)
		if decl.found {
			found = append(found, &Target{ImportPath: p.ImportPath, Dir: p.Dir, HasSub: decl.hasSub, ReadsSize: decl.readsSize})
		}
	}
	switch len(found) {
	case 0:
		where := fmt.Sprintf("%d packages under %s", len(pkgs), s.o.Dir)
		if s.o.Package != "" {
			where = "package " + s.o.Package
		}
		return nil, &Failure{Code: contract.NoBench, Msg: fmt.Sprintf("no func %s(b *testing.B) in the test files of %s", s.o.Bench, where)}
	case 1:
		s.target = found[0]
		return found[0], nil
	}
	var names []string
	for _, t := range found {
		names = append(names, t.ImportPath)
	}
	sort.Strings(names)
	return nil, &Failure{Code: contract.NoBench, Msg: fmt.Sprintf("%s is declared in %d packages (%s); choose one with -pkg or the package key", s.o.Bench, len(found), strings.Join(names, ", "))}
}

// decl is what scanPackage learns about a benchmark.
type decl struct {
	found, hasSub, readsSize bool
	benchmarks               int // benchmark functions in the package
}

// scanPackage parses the test files of a package and looks for
// func bench(b *testing.B). Files that do not parse are skipped: compiling
// the package reports them.
func scanPackage(files []string, bench string) decl {
	var d decl
	fset := token.NewFileSet()
	for _, path := range files {
		f, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if err != nil {
			continue
		}
		for _, x := range f.Decls {
			fd, ok := x.(*ast.FuncDecl)
			if !ok || fd.Recv != nil || !isBenchmark(fd) {
				continue
			}
			d.benchmarks++
			if fd.Name.Name == bench && fd.Body != nil {
				d.found = true
				d.hasSub = callsRun(fd)
			}
		}
		ast.Inspect(f, func(n ast.Node) bool {
			if lit, ok := n.(*ast.BasicLit); ok && lit.Kind == token.STRING {
				if v, err := strconv.Unquote(lit.Value); err == nil && v == SizeEnv {
					d.readsSize = true
				}
			}
			return !d.readsSize
		})
	}
	return d
}

// isBenchmark reports whether fd looks like func BenchmarkXxx(b *testing.B).
func isBenchmark(fd *ast.FuncDecl) bool {
	name := fd.Name.Name
	if !strings.HasPrefix(name, "Benchmark") {
		return false
	}
	if rest := name[len("Benchmark"):]; rest != "" && rest[0] >= 'a' && rest[0] <= 'z' {
		return false
	}
	params := fd.Type.Params.List
	if len(params) != 1 {
		return false
	}
	star, ok := params[0].Type.(*ast.StarExpr)
	if !ok {
		return false
	}
	sel, ok := star.X.(*ast.SelectorExpr)
	return ok && sel.Sel.Name == "B"
}

// callsRun reports whether the benchmark body calls b.Run on its parameter.
func callsRun(fd *ast.FuncDecl) bool {
	params := fd.Type.Params.List
	if len(params[0].Names) == 0 {
		return false
	}
	b := params[0].Names[0].Name
	found := false
	ast.Inspect(fd.Body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return !found
		}
		if sel, ok := call.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "Run" {
			if id, ok := sel.X.(*ast.Ident); ok && id.Name == b {
				found = true
			}
		}
		return !found
	})
	return found
}

// Survey counts packages and benchmark functions under the project
// directory. It names nothing, so preflight can print it.
type Survey struct {
	Packages          int
	PackagesWithError int
	PackagesWithBench int
	Benchmarks        int
	ErrorCode         contract.Code // code of the first package error, "" if none
}

// Survey lists every package under the project directory.
func (s *Session) Survey(ctx context.Context) (*Survey, error) {
	pkgs, err := s.list(ctx, "./...")
	if err != nil {
		return nil, err
	}
	sv := &Survey{Packages: len(pkgs)}
	for _, p := range pkgs {
		if p.Error != nil {
			sv.PackagesWithError++
			if sv.ErrorCode == "" {
				sv.ErrorCode = classifyBuild(p.Error.Err)
			}
		}
		d := scanPackage(p.testFiles(), "")
		if d.benchmarks > 0 {
			sv.PackagesWithBench++
			sv.Benchmarks += d.benchmarks
		}
	}
	return sv, nil
}
