package testigogen

import (
	"fmt"
	"go/types"
)

func (g *generator) renderStub(entry artifact, fixture artifact, errorMode bool) error {
	iface := types.Unalias(entry.typeOf).Underlying().(*types.Interface)
	iface.Complete()
	methods := make([]methodModel, 0, iface.NumMethods())
	for i := range iface.NumMethods() {
		method := iface.Method(i)
		if !method.Exported() && method.Pkg() != nil && method.Pkg().Path() != g.model.pkg.PkgPath {
			return fmt.Errorf("testigo-gen: %s: cannot implement unexported method %s from another package", entry.name, method.Name())
		}
		built, err := buildMethod(method.Name(), method.Type().(*types.Signature))
		if err != nil {
			return fmt.Errorf("testigo-gen: %s.%s: %w", entry.name, method.Name(), err)
		}
		methods = append(methods, built)
	}

	name := entry.name + "Stub"
	if errorMode {
		name = entry.name + "ErrorStub"
	}
	fixtureBuilder := lowerFirst(fixture.name) + "Builder"
	interfaceName := g.imports.typeString(entry.typeOf)

	if errorMode {
		fmt.Fprintf(&g.body, "// %s implements %s and returns the configured error.\n", name, interfaceName)
	} else {
		fmt.Fprintf(&g.body, "// %s implements %s using values from the supplied fixture.\n", name, interfaceName)
	}
	fmt.Fprintf(&g.body, "type %s struct {\n\tbase %s\n", name, fixtureBuilder)
	if errorMode {
		g.body.WriteString("\terr  error\n")
	}
	g.body.WriteString("}\n\n")
	fmt.Fprintf(&g.body, "var _ %s = (*%s)(nil)\n\n", interfaceName, name)
	for _, method := range methods {
		g.renderStubMethod(name, method, fixture.typeOf, errorMode)
	}
	if errorMode {
		fmt.Fprintf(&g.body, "// New%s creates an error stub backed by the supplied fixture.\n", name)
		fmt.Fprintf(&g.body, "func New%s(base %s, err error) *%s { return &%s{base: base, err: err} }\n\n", name, fixtureBuilder, name, name)
	} else {
		fmt.Fprintf(&g.body, "// New%s creates a stub backed by the supplied fixture.\n", name)
		fmt.Fprintf(&g.body, "func New%s(base %s) *%s { return &%s{base: base} }\n\n", name, fixtureBuilder, name, name)
	}
	return nil
}

func (g *generator) renderStubMethod(name string, method methodModel, fixtureType types.Type, errorMode bool) {
	fmt.Fprintf(&g.body, "func (s *%s) %s(%s)%s {\n", name, method.name, g.renderParams(method.params), g.renderResults(method.results))
	for i, result := range method.results {
		if errorMode && types.Identical(result, types.Universe.Lookup("error").Type()) {
			fmt.Fprintf(&g.body, "\tvar result%d %s = s.err\n", i, g.imports.typeString(result))
		} else if types.AssignableTo(fixtureType, result) {
			fmt.Fprintf(&g.body, "\tresult%d := s.base.Bare()\n", i)
		} else {
			fmt.Fprintf(&g.body, "\tvar result%d %s\n", i, g.imports.typeString(result))
		}
	}
	if len(method.results) > 0 {
		results := make([]string, len(method.results))
		for i := range results {
			results[i] = fmt.Sprintf("result%d", i)
		}
		fmt.Fprintf(&g.body, "\treturn %s\n", joinComma(results))
	}
	g.body.WriteString("}\n\n")
}

func joinComma(values []string) string {
	result := ""
	for i, value := range values {
		if i > 0 {
			result += ", "
		}
		result += value
	}
	return result
}
