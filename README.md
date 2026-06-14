# testigo

> **Not another testing lib.**

`testigo` is a hand-written test-double toolkit for Go. You write your own spies
and stubs as plain structs; `testigo` gives you the power to observe them, assert
on them, and build the test data around them — without ever taking control away
from the code you write. The doubles are your structs; `testigo` only watches and
verifies, favoring immutability in a simple, highly readable way.

[![PkgGoDev](https://pkg.go.dev/badge/github.com/lautaromei/testigo)](https://pkg.go.dev/github.com/lautaromei/testigo)

Features:

- **Hand-written doubles** — spies you write, no code generation. Method
  *references* (`mailer.Send`), not strings — rename-safe and IDE-aware.
- **Demanding assertions** — every recorded call must be accounted for, and
  verifying a call also demands asserting its outcome. The library refuses to let
  you forget.
- **Automatic restore between subtests** — register once, restored to an immutable
  baseline before each subtest. No `BeforeEach`/`AfterEach`.
- **Object-mother fixtures and reproducible random data** — the noisy half of a
  test, made readable.

Getting started:

- [Installation](#installation)
- [API documentation](https://pkg.go.dev/github.com/lautaromei/testigo)
- [Wiki — full guide with examples](https://github.com/lautaromei/testigo/wiki)

---

## `testigo` — hand-written doubles

A double is a normal struct that embeds `testigo.Spy`. The **first line** of every
spied method is `Call(args...)` — that is what records the interaction.

```go
type MailerSpy struct {
	testigo.Spy
}

func (m *MailerSpy) Send(to string) error {
	m.Call(to)
	return nil
}
```

`NewDouble` registers the double and restores it between subtests; `Run` runs a
subtest with everything pristine again. It reads like a sentence:

```go
var users = fixture.New(User{Name: "Ada", Email: "ada@mail.test"})
var asGrace = func(u User) User { u.Name = "Grace"; return u }

func TestGreeter(t *testing.T) {
	mailer := testigo.NewDouble(t, &MailerSpy{})

	testigo.Run(t, "mails a welcome to the user", func(t *testing.T) {
		u := users.With(asGrace)
		g := Greeter{mailer: mailer}

		msg, err := g.Welcome(u)

		assert.NoError(t, err)
		assert.Equal(t, msg, "Hello, Grace")
		assert.That(g).Called(mailer.Send).WithParams(u.Email)
	})
}
```

## `testigo/assert` — demanding assertions

Every interaction check is a sentence-shaped chain that starts at `That(caller)` —
it always states *who* called whom. A method *reference*, not a string.

```go
assert.That(g).Called(mailer.Send)                  // exactly once (the default)
assert.That(g).Called(mailer.Send).Twice()          // exactly twice
assert.That(g).Called(mailer.Send).Times(7)         // exactly n times
assert.That(g).Called(mailer.Send).Never()          // not called at all
assert.That(g).Called(mailer.Send).AtLeastOnce()    // one or more times
assert.That(g).Called(mailer.Send).WithParams("ada@mail.test") // match the arguments
assert.Expect(t).That(g).Called(mailer.Send)        // explicit handle (your own goroutines)
```

It is demanding on purpose — that is what keeps tests honest as the code changes:

- **Every call is accounted for.** A call that happened but was never asserted
  fails the test as an *unexpected call*, with the full call graph. You can't
  silently grow behavior the tests don't see.
- **Verifying a call demands asserting its outcome.** A `Called` chain alone isn't
  enough; `testigo` also requires at least one result/state assertion (`Equal`,
  `NoError`, `Len`, `DidChange`, ...). No "interaction-only" tests that prove *who
  was called* but never check *what came out*.
- **A forgotten `Call` is caught** — `testigo` notices a double that holds a `Spy`
  but recorded nothing and tells you which method to fix.

Arguments are **deep-copied at call time**, so a slice/map/pointer the subject
mutates *after* the call still matches what the double actually received. The same
package carries the plain value asserts and state checks:

```go
assert.Equal(t, got, want)       // generic: cross-type compare is a compile error
assert.NoError(t, err)           // t.Fatal when err != nil
assert.Len(t, items, 3)
assert.That(provider).DidChange() // a non-Spy field differs from registration
```

> The `require`-style "stop now" behavior is built in: `NoError`/`Error` call
> `t.Fatal`; `Equal`/`True`/`False` report and continue.

## `testigo/fixture` — object mothers

Test data is the other noisy half. Keep one canonical base, derive variants by
applying small named variations. Every variant is a deep copy — the base and its
siblings are never touched, even with slices, maps or pointers.

```go
var users = fixture.New(User{Name: "Ada", Email: "ada@mail.test"})

var asGrace = func(u User) User { u.Name = "Grace"; return u }
var asVIP   = func(u User) User { u.VIP = true; return u }

grace    := users.With(asGrace)                      // one variant
vipGrace := users.With(asGrace, asVIP)               // composed, in order
many     := users.Times(3, asVIP)                    // 3 independent variants
maybeVIP := users.With(fixture.Maybe(asVIP))         // applied ~half the runs
either   := users.With(fixture.OneOf(asGrace, asVIP)) // one of them, at random
```

## `testigo/random` — reproducible test data

Throwaway data that is distinct enough to avoid collisions, small enough to read
in a failure, and reproducible under a fixed seed (printed on failure as a readable
phrase, replayable via `TESTIGO_SEED`).

```go
func TestMain(m *testing.M) {
	testigo.Seed("river-stone-maple-frost") // seed once, before any draw
	os.Exit(m.Run())
}

name  := random.Name()
email := random.Email()
when  := random.AnyDateAfterNow()
pick  := random.Pick("a", "b", "c")
```

## Restore between subtests — no `BeforeEach`/`AfterEach`

`NewDouble(t, x)` snapshots `x` as an **immutable baseline**. Before every
`testigo.Run`, each registered double is restored: in-memory state deep-copied
back, any `Spy` cleared, the wiring between doubles kept by identity. You mutate a
double freely inside a subtest, and the next one starts pristine — no copy-on-write
authoring, no teardown to write.

```go
func TestRestore(t *testing.T) {
	inbox := testigo.NewDouble(t, &Inbox{}) // a plain double, no Spy

	testigo.Run(t, "mutates the double freely", func(t *testing.T) {
		inbox.Add("one")
		inbox.Add("two")
		assert.Equal(t, len(inbox.messages), 2)
	})

	testigo.Run(t, "next subtest starts pristine", func(t *testing.T) {
		assert.Equal(t, len(inbox.messages), 0) // deep-copied back from the baseline
	})
}
```

Shared mutable state between tests is the biggest source of order-dependent
flakiness, and the usual answer — a `BeforeEach`/`AfterEach` pair you keep in sync
by hand — is boilerplate you have to *remember* (and an AI assistant is prone to
drop). `testigo` removes those hooks: the **library**, not your test, guarantees
every subtest sees a fresh copy of the immutable baseline.

For state a struct copy can't reach — a database, a temp dir, a file — a double
implements `Resetter` (a single `Reset()` method) and `testigo` calls it at every
reset point. Any double embedding `Spy` already satisfies it for free.

```go
func (s *FileStore) Reset() { /* wipe + re-seed the external state */ }
```

---

## Installation

```bash
go get github.com/lautaromei/testigo
```

Then import the root package plus whichever subpackages you use:

```go
import (
	"github.com/lautaromei/testigo"          // doubles: Spy, Call, NewDouble, Run, Reset
	"github.com/lautaromei/testigo/assert"   // assertions: That/Called, Equal, NoError, ...
	"github.com/lautaromei/testigo/fixture"  // object mothers: New, With, Times, OneOf, ...
	"github.com/lautaromei/testigo/random"   // reproducible test data
)
```

## Staying up to date

```bash
go get -u github.com/lautaromei/testigo
```

## Supported Go versions

`testigo` supports Go 1.25 and onward.

## License

MIT — see [LICENSE](LICENSE).
