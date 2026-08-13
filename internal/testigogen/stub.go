package testigogen

import (
	"fmt"
	"go/types"
)

func (g *generator) renderStub(entry artifact, fixture *artifact, errorMode bool) error {
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
	interfaceName := g.imports.typeString(entry.typeOf)

	if errorMode {
		fmt.Fprintf(&g.body, "// %s implements %s and returns the configured error.\n", name, interfaceName)
	} else {
		fmt.Fprintf(&g.body, "// %s implements %s using values from the supplied fixture.\n", name, interfaceName)
	}
	fmt.Fprintf(&g.body, "type %s struct {\n", name)
	if fixture != nil {
		fmt.Fprintf(&g.body, "\tbase %s\n", lowerFirst(fixture.name)+"Builder")
	}
	if errorMode {
		g.body.WriteString("\terr  error\n")
	}
	g.body.WriteString("}\n\n")
	fmt.Fprintf(&g.body, "var _ %s = (*%s)(nil)\n\n", interfaceName, name)
	for _, method := range methods {
		var fixtureType types.Type
		if fixture != nil {
			fixtureType = fixture.typeOf
		}
		g.renderStubMethod(name, method, fixtureType, entry.collections, entry.found, errorMode)
	}
	if errorMode {
		if fixture == nil {
			fmt.Fprintf(&g.body, "// New%s creates an error stub.\n", name)
			fmt.Fprintf(&g.body, "func New%s(err error) *%s { return &%s{err: err} }\n\n", name, name, name)
		} else {
			fixtureBuilder := lowerFirst(fixture.name) + "Builder"
			fmt.Fprintf(&g.body, "// New%s creates an error stub backed by the supplied fixture.\n", name)
			fmt.Fprintf(&g.body, "func New%s(base %s, err error) *%s { return &%s{base: base, err: err} }\n\n", name, fixtureBuilder, name, name)
		}
	} else {
		fixtureBuilder := lowerFirst(fixture.name) + "Builder"
		fmt.Fprintf(&g.body, "// New%s creates a stub backed by the supplied fixture.\n", name)
		fmt.Fprintf(&g.body, "func New%s(base %s) *%s { return &%s{base: base} }\n\n", name, fixtureBuilder, name, name)
	}
	return nil
}

func (g *generator) renderStubMethod(name string, method methodModel, fixtureType types.Type, collections int, found bool, errorMode bool) {
	fmt.Fprintf(&g.body, "func (s *%s) %s(%s)%s {\n", name, method.name, g.renderParams(method.params), g.renderResults(method.results))
	for i, result := range method.results {
		if errorMode && types.Identical(result, types.Universe.Lookup("error").Type()) {
			fmt.Fprintf(&g.body, "\tvar result%d %s = s.err\n", i, g.imports.typeString(result))
		} else if fixtureType != nil && types.AssignableTo(fixtureType, result) {
			fmt.Fprintf(&g.body, "\tresult%d := s.base.Bare()\n", i)
		} else if fixtureType != nil && collections > 0 && sliceAccepts(result, fixtureType) {
			fmt.Fprintf(&g.body, "\tresult%d := %s{", i, g.imports.typeString(result))
			for item := 0; item < collections; item++ {
				if item > 0 {
					g.body.WriteString(", ")
				}
				g.body.WriteString("s.base.Bare()")
			}
			g.body.WriteString("}\n")
		} else if found && isBool(result) {
			fmt.Fprintf(&g.body, "\tvar result%d %s = true\n", i, g.imports.typeString(result))
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
