package core

import (
	"fmt"
	"reflect"
	"strings"
)

const (
	bold   = "\033[1m"
	red    = "\033[31m"
	green  = "\033[32m"
	yellow = "\033[33m"
	white  = "\033[37m"
	reset  = "\033[0m"
)

// DrawGraph renders the recorded calls as a tree, one root per origin.
func (m *Spy) DrawGraph(commonPackage string) string {
	if m.mu != nil {
		m.mu.RLock()
		defer m.mu.RUnlock()
	}

	if len(m.calls) == 0 {
		return "Call Graph: No calls recorded."
	}

	var b strings.Builder
	if commonPackage != "" {
		fmt.Fprintf(&b, "Call Graph (package: %s):\n\n", commonPackage)
	} else {
		b.WriteString("Call Graph:\n\n")
	}

	g := newGraphRenderer(m, commonPackage != "")

	for i, root := range g.roots() {
		if i > 0 {
			b.WriteString("\n")
		}
		fmt.Fprintf(&b, "  %s\n", g.callerLabel(root))
		for _, line := range g.renderChildren(root) {
			fmt.Fprintf(&b, "   %s\n", line)
		}
	}

	return b.String()
}

type graphRenderer struct {
	spy          *Spy
	strip        bool
	children     map[string][]*CallRecord
	callerOrder  []string
	calleeSet    map[string]bool
	visited      map[*CallRecord]bool
	unexpected   map[*CallRecord]bool
	usedFailures map[*failureRecord]bool
}

func newGraphRenderer(m *Spy, strip bool) *graphRenderer {
	g := &graphRenderer{
		spy:          m,
		strip:        strip,
		children:     make(map[string][]*CallRecord),
		calleeSet:    make(map[string]bool),
		visited:      make(map[*CallRecord]bool),
		unexpected:   make(map[*CallRecord]bool),
		usedFailures: make(map[*failureRecord]bool),
	}

	for _, call := range m.calls {
		caller := call.CallerComponent + "." + call.CallerMethod
		if _, seen := g.children[caller]; !seen {
			g.callerOrder = append(g.callerOrder, caller)
		}
		g.children[caller] = append(g.children[caller], call)
		g.calleeSet[call.CalleeComponent+"."+call.CalleeMethod] = true
	}

	for _, call := range m.unexpectedCalls {
		g.unexpected[call] = true
	}
	return g
}

func (g *graphRenderer) roots() []string {
	var roots []string
	for _, caller := range g.callerOrder {
		if !g.calleeSet[caller] {
			roots = append(roots, caller)
		}
	}
	if len(roots) == 0 {
		return g.callerOrder
	}
	return roots
}

func (g *graphRenderer) renderChildren(caller string) []string {
	type block struct {
		head  string
		sub   []string
		count int
	}

	var blocks []block
	for _, call := range g.children[caller] {
		if g.visited[call] {
			continue
		}
		g.visited[call] = true

		head, notes := g.renderNode(call)
		sub := make([]string, 0, len(notes))
		for _, note := range notes {
			sub = append(sub, "  "+note)
		}
		sub = append(sub, g.renderChildren(call.CalleeComponent+"."+call.CalleeMethod)...)

		if n := len(blocks); n > 0 && blocks[n-1].head == head &&
			strings.Join(blocks[n-1].sub, "\n") == strings.Join(sub, "\n") {
			blocks[n-1].count++
			continue
		}
		blocks = append(blocks, block{head: head, sub: sub, count: 1})
	}

	var out []string
	for i, blk := range blocks {
		connector, continuation := "├─▶ ", "│    "
		if i == len(blocks)-1 {
			connector, continuation = "└─▶ ", "     "
		}

		head := blk.head
		if blk.count > 1 {
			head += fmt.Sprintf(" %s(x%d)%s", green, blk.count, reset)
		}

		out = append(out, connector+head)
		for _, s := range blk.sub {
			out = append(out, continuation+s)
		}
	}
	return out
}

func (g *graphRenderer) renderNode(call *CallRecord) (string, []string) {
	name := cleanFuncName(call.CalleeComponent+"."+call.CalleeMethod, g.strip)

	params := ""
	if recorded := call.recorded(); len(recorded) > 0 {
		parts := make([]string, len(recorded))
		for i, p := range recorded {
			parts[i] = fmt.Sprintf("%v", p)
		}
		params = "(" + strings.Join(parts, ", ") + ")"
	}

	if g.unexpected[call] {
		return fmt.Sprintf("%s%s%s%s%s   %s✘ unexpected call%s", red, bold, name, params, reset, red, reset), nil
	}

	if notes := g.failureNotes(call); len(notes) > 0 {
		return fmt.Sprintf("%s%s%s%s%s%s%s", red, bold, name, reset, red, params, reset), notes
	}

	lastDot := strings.LastIndex(name, ".")
	return name[:lastDot+1] + bold + name[lastDot+1:] + reset + params, nil
}

func (g *graphRenderer) failureNotes(call *CallRecord) []string {
	for _, failure := range g.spy.failures {
		if g.usedFailures[failure] || failure.failedAssertion == nil {
			continue
		}

		matches := failure.mismatchedCall == call ||
			(failure.mismatchedCall == nil && call.CalleeMethod == failure.failedAssertion.funcName)
		if !matches {
			continue
		}
		g.usedFailures[failure] = true
		location := strings.TrimSuffix(call.location(), ": ")

		switch {
		case strings.Contains(failure.reason, "called by"):
			failure.annotated = true
			return []string{
				fmt.Sprintf("%s✘ expected caller: %s%s%s", red, g.stripCaller(failure.failedAssertion.callerComponent), at(location), reset),
				fmt.Sprintf("%s✓ actual caller:   %s%s", green, g.stripCaller(call.CallerComponent), reset),
			}
		case strings.Contains(failure.reason, "different arguments"):
			if notes := paramDiffNotes(failure.failedAssertion.expectedArgs, call.recorded()); len(notes) > 0 {
				failure.annotated = true
				notes[0] = fmt.Sprintf("%s✘ params differ%s:%s", red, at(location), reset)
				return notes
			}
			return []string{
				fmt.Sprintf("%s✘ expected params: %s%s", red, formatArgs(failure.failedAssertion.expectedArgs), reset),
			}
		default:
			failure.annotated = true
			return []string{
				fmt.Sprintf("%s✘ expected %s, got x%d%s%s", red, timesLabel(failure.failedAssertion), failure.actualCount, at(location), reset),
			}
		}
	}
	return nil
}

func paramDiffNotes(expected, actual []any) []string {
	if len(expected) == 0 || len(expected) != len(actual) {
		return nil
	}

	var diffs []valueDiff
	for i, exp := range expected {
		if exp == Anything {
			continue
		}
		if _, ok := exp.(Matcher); ok {
			return nil
		}
		prefix := ""
		if len(expected) > 1 {
			prefix = fmt.Sprintf("arg%d", i)
		}
		diffValues(reflect.ValueOf(actual[i]), reflect.ValueOf(exp), prefix, &diffs)
	}
	if len(diffs) == 0 {
		return nil
	}

	notes := make([]string, 0, len(diffs)+1)
	notes = append(notes, fmt.Sprintf("%s✘ params differ:%s", red, reset))
	for _, d := range diffs {
		path := d.path
		if path == "" {
			path = "value"
		}
		notes = append(notes, fmt.Sprintf("  %s%s%s: got %s%s%s, want %s%s%s", bold, path, reset, red, d.got, reset, green, d.want, reset))
	}
	return notes
}

func at(location string) string {
	if location == "" {
		return ""
	}
	return " at " + location
}

func timesLabel(a *CalledFunc) string {
	if a.atLeast {
		return fmt.Sprintf("at least x%d", a.times)
	}
	return fmt.Sprintf("x%d", a.times)
}

func (g *graphRenderer) stripCaller(name string) string {
	if !g.strip {
		return name
	}
	if parts := strings.Split(name, "."); len(parts) > 1 {
		return strings.Join(parts[1:], ".")
	}
	return name
}

func (g *graphRenderer) callerLabel(caller string) string {
	name := cleanFuncName(caller, g.strip)
	lastDot := strings.LastIndex(name, ".")
	return name[:lastDot+1] + bold + name[lastDot+1:] + reset
}

func formatArgs(args []any) string {
	if len(args) == 0 {
		return "(no arguments)"
	}
	return fmt.Sprintf("%v", args)
}
