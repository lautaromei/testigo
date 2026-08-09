package testigogen

import (
	"fmt"
	"go/types"
	"strings"
)

func (g *generator) renderSeeder(entry artifact) error {
	selection := lookupMethod(entry.typeOf, entry.seederMethod)
	signature := selection.Obj().Type().(*types.Signature)
	if signature.Variadic() {
		return fmt.Errorf("testigo-gen: %s.%s: variadic repository methods are not supported", entry.name, entry.seederMethod)
	}

	method, err := buildMethod(entry.seederMethod, signature)
	if err != nil {
		return fmt.Errorf("testigo-gen: %s.%s: %w", entry.name, entry.seederMethod, err)
	}
	entityIndex := -1
	var entityType types.Type
	entityPointer := false
	for i, param := range method.params {
		candidate := param.typeOf
		pointer := false
		if ptr, ok := types.Unalias(candidate).(*types.Pointer); ok {
			candidate = ptr.Elem()
			pointer = true
		}
		if _, _, ok := namedStruct(candidate); !ok {
			continue
		}
		if entityIndex >= 0 {
			return fmt.Errorf("testigo-gen: %s.%s: cannot infer entity from more than one struct parameter", entry.name, entry.seederMethod)
		}
		entityIndex = i
		entityType = candidate
		entityPointer = pointer
	}
	if entityIndex < 0 {
		return fmt.Errorf("testigo-gen: %s.%s: cannot infer an entity struct parameter", entry.name, entry.seederMethod)
	}

	reserved := map[string]bool{"t": true, "repository": true, "value": true}
	constructorParams := make([]parameter, 0, len(method.params)-1)
	for i, param := range method.params {
		if i == entityIndex {
			continue
		}
		name := param.name
		for reserved[name] {
			name += "Arg"
		}
		reserved[name] = true
		param.name = name
		method.params[i].name = name
		constructorParams = append(constructorParams, param)
	}

	errorIndexes := make([]int, 0, 1)
	errorType := types.Universe.Lookup("error").Type()
	for i, result := range method.results {
		if types.AssignableTo(result, errorType) {
			errorIndexes = append(errorIndexes, i)
		}
	}
	if len(errorIndexes) > 1 {
		return fmt.Errorf("testigo-gen: %s.%s: multiple error results are not supported", entry.name, entry.seederMethod)
	}

	testingAlias := g.imports.add("testing", "testing")
	seederAlias := g.imports.add("github.com/lautaromei/testigo/seeder", "seeder")
	repositoryType := g.imports.typeString(entry.typeOf)
	entityName := g.imports.typeString(entityType)
	constructor := "New" + entry.name + "Seeder"

	params := []string{"t " + testingAlias + ".TB", "repository " + repositoryType}
	for _, param := range constructorParams {
		params = append(params, param.name+" "+g.imports.typeString(param.typeOf))
	}
	fmt.Fprintf(&g.body, "// %s adapts %s.%s to a typed seeder.\n", constructor, repositoryType, entry.seederMethod)
	fmt.Fprintf(&g.body, "func %s(%s) %s.Seeder[%s] {\n", constructor, strings.Join(params, ", "), seederAlias, entityName)
	fmt.Fprintf(&g.body, "\treturn %s.New(t, func(value %s) error {\n", seederAlias, entityName)

	callArgs := make([]string, len(method.params))
	for i, param := range method.params {
		if i == entityIndex {
			callArgs[i] = "value"
			if entityPointer {
				callArgs[i] = "&value"
			}
		} else {
			callArgs[i] = param.name
		}
	}
	call := fmt.Sprintf("repository.%s(%s)", entry.seederMethod, strings.Join(callArgs, ", "))
	switch {
	case len(method.results) == 1 && len(errorIndexes) == 1:
		fmt.Fprintf(&g.body, "\t\treturn %s\n", call)
	case len(errorIndexes) == 1:
		assignments := make([]string, len(method.results))
		for i := range assignments {
			assignments[i] = "_"
		}
		assignments[errorIndexes[0]] = "err"
		fmt.Fprintf(&g.body, "\t\t%s := %s\n", strings.Join(assignments, ", "), call)
		g.body.WriteString("\t\treturn err\n")
	default:
		fmt.Fprintf(&g.body, "\t\t%s\n", call)
		g.body.WriteString("\t\treturn nil\n")
	}
	g.body.WriteString("\t})\n}\n\n")
	return nil
}
