package testigogen

import (
	"fmt"
	"go/token"
	"go/types"
	"path"
	"sort"
	"strings"
)

type importSet struct {
	current  string
	byPath   map[string]string
	byAlias  map[string]string
	defaults map[string]string
}

func newImportSet(current string) *importSet {
	return &importSet{
		current:  current,
		byPath:   make(map[string]string),
		byAlias:  make(map[string]string),
		defaults: make(map[string]string),
	}
}

func (s *importSet) qualifier(pkg *types.Package) string {
	if pkg == nil || pkg.Path() == s.current {
		return ""
	}
	return s.add(pkg.Path(), pkg.Name())
}

func (s *importSet) add(importPath, preferred string) string {
	if importPath == "" || importPath == s.current {
		return ""
	}
	if alias, ok := s.byPath[importPath]; ok {
		return alias
	}
	if preferred == "" {
		preferred = path.Base(importPath)
	}
	preferred = sanitizeIdentifier(preferred)
	s.defaults[importPath] = preferred
	alias := preferred
	for suffix := 2; ; suffix++ {
		usedBy, used := s.byAlias[alias]
		if !used || usedBy == importPath {
			break
		}
		alias = fmt.Sprintf("%s%d", preferred, suffix)
	}
	s.byPath[importPath] = alias
	s.byAlias[alias] = importPath
	return alias
}

func (s *importSet) typeString(value types.Type) string {
	return types.TypeString(value, s.qualifier)
}

func (s *importSet) render() string {
	if len(s.byPath) == 0 {
		return ""
	}
	var standard, external []string
	for importPath := range s.byPath {
		if strings.Contains(strings.Split(importPath, "/")[0], ".") {
			external = append(external, importPath)
		} else {
			standard = append(standard, importPath)
		}
	}
	sort.Strings(standard)
	sort.Strings(external)
	paths := append(standard, external...)

	var out strings.Builder
	out.WriteString("import (\n")
	for i, importPath := range paths {
		if i == len(standard) && len(standard) > 0 && len(external) > 0 {
			out.WriteString("\n")
		}
		alias := s.byPath[importPath]
		if alias == s.defaults[importPath] {
			fmt.Fprintf(&out, "\t%q\n", importPath)
		} else {
			fmt.Fprintf(&out, "\t%s %q\n", alias, importPath)
		}
	}
	out.WriteString(")\n\n")
	result := out.String()
	if token.Lookup(result).IsKeyword() {
		return result + "_"
	}
	return result
}

func sanitizeIdentifier(value string) string {
	if value == "" {
		return "value"
	}
	var out strings.Builder
	for i, r := range value {
		valid := r == '_' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || i > 0 && r >= '0' && r <= '9'
		if valid {
			out.WriteRune(r)
		} else {
			out.WriteByte('_')
		}
	}
	return out.String()
}

func lowerFirst(value string) string {
	if value == "" {
		return value
	}
	return strings.ToLower(value[:1]) + value[1:]
}
