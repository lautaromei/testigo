package testigogen

import (
	"fmt"
	"go/types"
	"reflect"
	"sort"
	"strings"

	"golang.org/x/tools/go/packages"
)

const tagName = "testigo"

// Options selects the package and tagged specification type to generate.
type Options struct {
	Dir      string
	SpecType string
}

type model struct {
	pkg       *packages.Package
	artifacts []artifact
}

type artifact struct {
	name         string
	typeOf       types.Type
	fixture      bool
	baseFunc     string
	memdbKey     string
	spy          bool
	seederMethod string
	stub         bool
	errorStub    bool
	stubFixture  string
}

func loadModel(opts Options) (*model, error) {
	if opts.Dir == "" {
		opts.Dir = "."
	}
	if opts.SpecType == "" {
		opts.SpecType = "testigoSpec"
	}

	cfg := &packages.Config{
		Dir: opts.Dir,
		Mode: packages.NeedName |
			packages.NeedFiles |
			packages.NeedCompiledGoFiles |
			packages.NeedSyntax |
			packages.NeedTypes |
			packages.NeedTypesInfo |
			packages.NeedImports |
			packages.NeedDeps,
	}
	pkgs, err := packages.Load(cfg, ".")
	if err != nil {
		return nil, fmt.Errorf("testigo-gen: load package: %w", err)
	}
	if len(pkgs) != 1 {
		return nil, fmt.Errorf("testigo-gen: expected one package in %s, got %d", opts.Dir, len(pkgs))
	}
	if count := packages.PrintErrors(pkgs); count > 0 {
		return nil, fmt.Errorf("testigo-gen: package has %d load error(s)", count)
	}
	pkg := pkgs[0]

	object := pkg.Types.Scope().Lookup(opts.SpecType)
	if object == nil {
		return nil, fmt.Errorf("testigo-gen: specification type %q not found in package %s", opts.SpecType, pkg.PkgPath)
	}
	typeName, ok := object.(*types.TypeName)
	if !ok {
		return nil, fmt.Errorf("testigo-gen: %q is not a type", opts.SpecType)
	}
	structure, ok := types.Unalias(typeName.Type()).Underlying().(*types.Struct)
	if !ok {
		return nil, fmt.Errorf("testigo-gen: specification %q must be a struct", opts.SpecType)
	}

	result := &model{pkg: pkg}
	seenNames := make(map[string]bool)
	for i := range structure.NumFields() {
		field := structure.Field(i)
		raw, ok := reflect.StructTag(structure.Tag(i)).Lookup(tagName)
		if !ok || strings.TrimSpace(raw) == "" || raw == "-" {
			continue
		}
		if field.Embedded() {
			return nil, fmt.Errorf("testigo-gen: specification field %s must be named", field.Name())
		}
		if seenNames[field.Name()] {
			return nil, fmt.Errorf("testigo-gen: duplicate artifact name %s", field.Name())
		}
		seenNames[field.Name()] = true

		entry, err := parseArtifact(field.Name(), field.Type(), raw)
		if err != nil {
			return nil, err
		}
		if err := validateArtifact(pkg, entry); err != nil {
			return nil, err
		}
		result.artifacts = append(result.artifacts, entry)
	}
	if len(result.artifacts) == 0 {
		return nil, fmt.Errorf("testigo-gen: specification %q has no %q tags", opts.SpecType, tagName)
	}
	fixtures := make(map[string]types.Type)
	for _, entry := range result.artifacts {
		if entry.fixture {
			fixtures[entry.name] = entry.typeOf
		}
	}
	for _, entry := range result.artifacts {
		if !entry.stub && !entry.errorStub {
			continue
		}
		fixtureType, ok := fixtures[entry.stubFixture]
		if !ok {
			return nil, fmt.Errorf("testigo-gen: %s: fixture %q not found", entry.name, entry.stubFixture)
		}
		if err := validateStubFixture(entry, fixtureType); err != nil {
			return nil, err
		}
	}
	sort.Slice(result.artifacts, func(i, j int) bool {
		return result.artifacts[i].name < result.artifacts[j].name
	})
	return result, nil
}

func parseArtifact(name string, typeOf types.Type, raw string) (artifact, error) {
	entry := artifact{name: name, typeOf: typeOf}
	for _, token := range strings.Split(raw, ",") {
		token = strings.TrimSpace(token)
		if token == "" {
			continue
		}
		key, value, hasValue := strings.Cut(token, "=")
		switch key {
		case "fixture":
			entry.fixture = true
		case "spy":
			entry.spy = true
		case "stub", "errorstub":
			if !hasValue || value == "" {
				return artifact{}, fmt.Errorf("testigo-gen: %s requires a fixture name", key)
			}
			entry.stubFixture = value
			if key == "stub" {
				entry.stub = true
			} else {
				entry.errorStub = true
			}
		case "base", "default":
			if !hasValue || value == "" {
				return artifact{}, fmt.Errorf("testigo-gen: %s requires a function name", key)
			}
			entry.baseFunc = value
		case "memdb":
			if !hasValue || value == "" {
				return artifact{}, fmt.Errorf("testigo-gen: memdb requires a key field")
			}
			entry.memdbKey = value
		case "seeder":
			if !hasValue || value == "" {
				return artifact{}, fmt.Errorf("testigo-gen: seeder requires a repository method")
			}
			entry.seederMethod = value
		default:
			return artifact{}, fmt.Errorf("testigo-gen: %s: unknown tag option %q", name, key)
		}
	}
	if !entry.fixture && !entry.spy && !entry.stub && !entry.errorStub && entry.seederMethod == "" {
		return artifact{}, fmt.Errorf("testigo-gen: %s: tag must request fixture, spy, errorstub, or seeder", name)
	}
	if entry.stub && entry.errorStub {
		return artifact{}, fmt.Errorf("testigo-gen: %s: stub and errorstub are mutually exclusive", name)
	}
	if (entry.baseFunc != "" || entry.memdbKey != "") && !entry.fixture {
		return artifact{}, fmt.Errorf("testigo-gen: %s: base/default and memdb require fixture", name)
	}
	return entry, nil
}

func validateArtifact(pkg *packages.Package, entry artifact) error {
	if entry.fixture {
		named, structure, ok := namedStruct(entry.typeOf)
		if !ok {
			return fmt.Errorf("testigo-gen: %s: fixture requires a named struct, got %s", entry.name, entry.typeOf)
		}
		if named.TypeArgs() != nil && named.TypeArgs().Len() > 0 {
			return fmt.Errorf("testigo-gen: %s: generic fixture types are not supported yet", entry.name)
		}
		if entry.baseFunc != "" {
			if err := validateBaseFunc(pkg, entry.baseFunc, entry.typeOf); err != nil {
				return fmt.Errorf("testigo-gen: %s: %w", entry.name, err)
			}
		}
		if entry.memdbKey != "" {
			field := findField(structure, entry.memdbKey)
			if field == nil {
				return fmt.Errorf("testigo-gen: %s: memdb key field %q not found", entry.name, entry.memdbKey)
			}
			if !types.Comparable(field.Type()) {
				return fmt.Errorf("testigo-gen: %s: memdb key field %q is not comparable", entry.name, entry.memdbKey)
			}
		}
	}
	if entry.spy {
		if _, ok := types.Unalias(entry.typeOf).Underlying().(*types.Interface); !ok {
			return fmt.Errorf("testigo-gen: %s: spy requires an interface; concrete types cannot be substituted", entry.name)
		}
	}
	if entry.stub || entry.errorStub {
		if _, ok := types.Unalias(entry.typeOf).Underlying().(*types.Interface); !ok {
			return fmt.Errorf("testigo-gen: %s: stub requires an interface", entry.name)
		}
	}
	if entry.seederMethod != "" {
		selection := lookupMethod(entry.typeOf, entry.seederMethod)
		if selection == nil {
			return fmt.Errorf("testigo-gen: %s: repository method %q not found", entry.name, entry.seederMethod)
		}
	}
	return nil
}

func validateStubFixture(entry artifact, fixtureType types.Type) error {
	if !isNamedStruct(fixtureType) {
		return fmt.Errorf("testigo-gen: %s: stub fixture %q must be a named struct", entry.name, entry.stubFixture)
	}
	iface := types.Unalias(entry.typeOf).Underlying().(*types.Interface)
	iface.Complete()
	hasFixtureResult := false
	hasErrorResult := false
	for i := range iface.NumMethods() {
		signature := iface.Method(i).Type().(*types.Signature)
		for j := range signature.Results().Len() {
			result := signature.Results().At(j).Type()
			hasFixtureResult = hasFixtureResult || types.AssignableTo(fixtureType, result)
			hasErrorResult = hasErrorResult || types.Identical(result, types.Universe.Lookup("error").Type())
		}
	}
	if entry.stub && !hasFixtureResult {
		return fmt.Errorf("testigo-gen: %s: stub fixture %q is not returned by any interface method", entry.name, entry.stubFixture)
	}
	if entry.errorStub && !hasErrorResult {
		return fmt.Errorf("testigo-gen: %s: errorstub interface must return error", entry.name)
	}
	return nil
}

func isNamedStruct(value types.Type) bool {
	_, _, ok := namedStruct(value)
	return ok
}

func validateBaseFunc(pkg *packages.Package, name string, want types.Type) error {
	object := pkg.Types.Scope().Lookup(name)
	function, ok := object.(*types.Func)
	if !ok {
		return fmt.Errorf("default %q must name a package function", name)
	}
	signature := function.Type().(*types.Signature)
	if signature.Params().Len() != 0 || signature.Results().Len() != 1 || !types.Identical(signature.Results().At(0).Type(), want) {
		return fmt.Errorf("default %q must have signature func() %s", name, want)
	}
	return nil
}

func namedStruct(value types.Type) (*types.Named, *types.Struct, bool) {
	named, ok := types.Unalias(value).(*types.Named)
	if !ok {
		return nil, nil, false
	}
	structure, ok := named.Underlying().(*types.Struct)
	return named, structure, ok
}

func findField(structure *types.Struct, name string) *types.Var {
	for i := range structure.NumFields() {
		if structure.Field(i).Name() == name {
			return structure.Field(i)
		}
	}
	return nil
}

func lookupMethod(value types.Type, name string) *types.Selection {
	set := types.NewMethodSet(value)
	for i := range set.Len() {
		selection := set.At(i)
		if selection.Obj().Name() == name {
			return selection
		}
	}
	return nil
}
