package testigogen

import (
	"fmt"
	"go/types"
	"strings"
)

type spyModel struct {
	fieldName     string
	typeName      string
	interfaceType types.Type
}

type parameter struct {
	name     string
	typeOf   types.Type
	variadic bool
}

type methodModel struct {
	name    string
	params  []parameter
	results []types.Type
}

func (g *generator) renderSpy(entry artifact) (spyModel, error) {
	iface := types.Unalias(entry.typeOf).Underlying().(*types.Interface)
	iface.Complete()
	spyName := entry.name + "Spy"
	methods := make([]methodModel, 0, iface.NumMethods())
	for i := range iface.NumMethods() {
		method := iface.Method(i)
		if !method.Exported() && method.Pkg() != nil && method.Pkg().Path() != g.model.pkg.PkgPath {
			return spyModel{}, fmt.Errorf("testigo-gen: %s: cannot implement unexported method %s from another package", entry.name, method.Name())
		}
		signature := method.Type().(*types.Signature)
		built, err := buildMethod(method.Name(), signature)
		if err != nil {
			return spyModel{}, fmt.Errorf("testigo-gen: %s.%s: %w", entry.name, method.Name(), err)
		}
		methods = append(methods, built)
	}

	testigoAlias := g.imports.add("github.com/lautaromei/testigo", "testigo")
	testingAlias := g.imports.add("testing", "testing")
	interfaceName := g.imports.typeString(entry.typeOf)

	fmt.Fprintf(&g.body, "// %s implements %s and records every call.\n", spyName, interfaceName)
	fmt.Fprintf(&g.body, "type %s struct {\n\t%s.Spy\n", spyName, testigoAlias)
	for _, method := range methods {
		fmt.Fprintf(&g.body, "\t%sFunc func(%s)%s\n", method.name, g.renderParams(method.params), g.renderResults(method.results))
	}
	g.body.WriteString("}\n\n")
	fmt.Fprintf(&g.body, "var _ %s = (*%s)(nil)\n\n", interfaceName, spyName)

	for _, method := range methods {
		g.renderSpyMethod(spyName, method)
	}

	fmt.Fprintf(&g.body, "// New%s creates and registers a pristine %s.\n", spyName, spyName)
	fmt.Fprintf(&g.body, "func New%s(t *%s.T, configure ...func(*%s)) *%s {\n", spyName, testingAlias, spyName, spyName)
	fmt.Fprintf(&g.body, "\tvalue := &%s{}\n", spyName)
	g.body.WriteString("\tfor _, configure := range configure {\n\t\tif configure != nil {\n\t\t\tconfigure(value)\n\t\t}\n\t}\n")
	fmt.Fprintf(&g.body, "\treturn %s.NewDouble(t, value)\n", testigoAlias)
	g.body.WriteString("}\n\n")

	return spyModel{fieldName: entry.name, typeName: spyName, interfaceType: entry.typeOf}, nil
}

func buildMethod(name string, signature *types.Signature) (methodModel, error) {
	if signature.TypeParams() != nil && signature.TypeParams().Len() > 0 {
		return methodModel{}, fmt.Errorf("generic methods are not supported")
	}
	used := map[string]bool{"s": true}
	params := make([]parameter, signature.Params().Len())
	for i := range signature.Params().Len() {
		item := signature.Params().At(i)
		paramName := item.Name()
		if paramName == "" || paramName == "_" || !validIdentifier(paramName) || used[paramName] {
			paramName = inferredParameterName(item.Type(), i)
		}
		for used[paramName] {
			paramName += "Value"
		}
		used[paramName] = true
		params[i] = parameter{
			name:     paramName,
			typeOf:   item.Type(),
			variadic: signature.Variadic() && i == signature.Params().Len()-1,
		}
	}
	results := make([]types.Type, signature.Results().Len())
	for i := range signature.Results().Len() {
		results[i] = signature.Results().At(i).Type()
	}
	return methodModel{name: name, params: params, results: results}, nil
}

func inferredParameterName(value types.Type, index int) string {
	if canonicalType(value) == "context.Context" {
		return "ctx"
	}
	candidate := types.Unalias(value)
	if pointer, ok := candidate.(*types.Pointer); ok {
		candidate = types.Unalias(pointer.Elem())
	}
	if named, ok := candidate.(*types.Named); ok {
		name := lowerFirst(named.Obj().Name())
		if validIdentifier(name) {
			return name
		}
	}
	return fmt.Sprintf("arg%d", index)
}

func (g *generator) renderSpyMethod(spyName string, method methodModel) {
	fmt.Fprintf(&g.body, "func (s *%s) %s(%s)%s {\n", spyName, method.name, g.renderParams(method.params), g.renderResults(method.results))
	callArgs := parameterNames(method.params, false)
	if callArgs == "" {
		g.body.WriteString("\ts.Call()\n")
	} else {
		fmt.Fprintf(&g.body, "\ts.Call(%s)\n", callArgs)
	}
	fmt.Fprintf(&g.body, "\tif s.%sFunc != nil {\n", method.name)
	funcArgs := parameterNames(method.params, true)
	if len(method.results) == 0 {
		fmt.Fprintf(&g.body, "\t\ts.%sFunc(%s)\n\t\treturn\n", method.name, funcArgs)
	} else {
		fmt.Fprintf(&g.body, "\t\treturn s.%sFunc(%s)\n", method.name, funcArgs)
	}
	g.body.WriteString("\t}\n")
	for i, result := range method.results {
		fmt.Fprintf(&g.body, "\tvar result%d %s\n", i, g.imports.typeString(result))
	}
	if len(method.results) == 0 {
		g.body.WriteString("\treturn\n")
	} else {
		results := make([]string, len(method.results))
		for i := range results {
			results[i] = fmt.Sprintf("result%d", i)
		}
		fmt.Fprintf(&g.body, "\treturn %s\n", strings.Join(results, ", "))
	}
	g.body.WriteString("}\n\n")
}

func (g *generator) renderParams(params []parameter) string {
	parts := make([]string, len(params))
	for i, param := range params {
		typeName := g.imports.typeString(param.typeOf)
		if param.variadic {
			if slice, ok := types.Unalias(param.typeOf).(*types.Slice); ok {
				typeName = "..." + g.imports.typeString(slice.Elem())
			}
		}
		parts[i] = param.name + " " + typeName
	}
	return strings.Join(parts, ", ")
}

func (g *generator) renderResults(results []types.Type) string {
	if len(results) == 0 {
		return ""
	}
	parts := make([]string, len(results))
	for i, result := range results {
		parts[i] = g.imports.typeString(result)
	}
	if len(parts) == 1 {
		return " " + parts[0]
	}
	return " (" + strings.Join(parts, ", ") + ")"
}

func parameterNames(params []parameter, expandVariadic bool) string {
	parts := make([]string, len(params))
	for i, param := range params {
		parts[i] = param.name
		if expandVariadic && param.variadic {
			parts[i] += "..."
		}
	}
	return strings.Join(parts, ", ")
}

func (g *generator) renderDoubles() {
	testigoAlias := g.imports.add("github.com/lautaromei/testigo", "testigo")
	testingAlias := g.imports.add("testing", "testing")
	g.body.WriteString("// Doubles groups every generated spy for one test.\n")
	g.body.WriteString("type Doubles struct {\n")
	for _, spy := range g.spies {
		fmt.Fprintf(&g.body, "\t%s *%s\n", spy.fieldName, spy.typeName)
	}
	g.body.WriteString("}\n\n")
	g.body.WriteString("// NewDoubles constructs, optionally configures, and registers every generated spy.\n")
	fmt.Fprintf(&g.body, "func NewDoubles(t *%s.T, configure ...func(*Doubles)) *Doubles {\n", testingAlias)
	g.body.WriteString("\tvalue := &Doubles{\n")
	for _, spy := range g.spies {
		fmt.Fprintf(&g.body, "\t\t%s: &%s{},\n", spy.fieldName, spy.typeName)
	}
	g.body.WriteString("\t}\n\tfor _, configure := range configure {\n\t\tif configure != nil {\n\t\t\tconfigure(value)\n\t\t}\n\t}\n")
	for _, spy := range g.spies {
		fmt.Fprintf(&g.body, "\tvalue.%s = %s.NewDouble(t, value.%s)\n", spy.fieldName, testigoAlias, spy.fieldName)
	}
	g.body.WriteString("\treturn value\n}\n\n")
}
