# testigo

> **Not another testing lib.**

`testigo` is a hand-written test-double toolkit for Go. You write your own spies
and stubs as plain structs; `testigo` gives you the power to observe them,
assert on them, and build the test data around them — without ever taking
control away from the code you write.

Now a test that uses **all the pieces** — `NewDouble`, a fixture, an extracted
variation, the validated response, and a `Called` assertion:

```go
import (
	"testing"

	"github.com/lautaromei/testigo"
	"github.com/lautaromei/testigo/assert"
	"github.com/lautaromei/testigo/fixture"
)

var user = fixture.New(User{Name: "Ada", Email: "ada@mail.test"})
func TestGreeter(t *testing.T) {
	mailer := testigo.NewDouble(t, &MailerSpy{})

	testigo.Run(t, "mails a welcome to the user", func(t *testing.T) {
		g := Greeter{mailer: mailer}

		msg, err := g.Welcome(user)

		assert.NoError(t, err)
		assert.That(g).Called(mailer.Send).WithParams(u.Email)
		assert.Equal(t, msg, "Hello, Grace")
	})
}
```
---

## Why not mocking libs?

Mainstream mocking libraries solve real problems, but they do it in a way that
stops scaling once a codebase grows. The cost is rarely visible on day one — it
shows up in the thousandth test, when the helpers, the generated code and the
indirection have quietly taken over.

Imagine instead that **every package could ship its own testable version** of its
types — real structs, with real behavior, that you compose like production code.
That is the direction `testigo` pushes you in favoring immutability in a simple, highly readable way.


---

## Our hero test double: the spy

A double is a normal struct that embeds `testigo.Spy`. The **first line** of every
spied method is `Call(args...)` — that is what records the interaction:

```go
type MailerSpy struct {
	testigo.Spy
}

func (m *MailerSpy) Send(to string) error {
	m.Call(to)
	return nil
}
```

The subject under test takes that double as a dependency — here a `Greeter` that
builds a greeting and asks the mailer to deliver it:

```go
type Mailer interface {
	Send(to string) error
}

type User struct {
	Name  string
	Email string
}

type Greeter struct {
	mailer Mailer
}

func (g Greeter) Welcome(u User) (string, error) {
	msg := "Hello, " + u.Name
	if err := g.mailer.Send(u.Email); err != nil {
		return "", err
	}
	return msg, nil
}
```

## Asserts are demanding — on purpose

This is the core idea, and it is intentionally strict.

Because every spied method records itself with `Call`, `testigo` knows about
**every** call that happened. At the end of the test it requires that each
recorded call is *accounted for* by an assertion. A call that happened but was
never asserted **fails the test** as an unexpected call, with the full call graph:

```
Call Graph (package: greeter):

  Greeter.Welcome
   └─▶ MailerSpy.Send(ada@mail.test)   ✘ unexpected call

found 1 error(s) during expectation assertion:
- found 1 unexpected call(s):
- greeter.go:26: unexpected call: greeter.MailerSpy.Send[ada@mail.test]
```

Why make tests demanding? Because it is what keeps them honest **as the code
changes**:

- Add a new interaction in production code and forget to assert it → the test
  fails. You can't silently grow behavior the tests don't see.
- Remove or rename an interaction → the method reference stops compiling. No
  string drifts out of sync.
- Forget a `Call` in a spied method → `testigo` notices a double that holds a
  `Spy` but recorded nothing and tells you exactly which method to fix.

The result: changes and omissions stay **covered** by definition. You don't have
to remember to assert every call — the library refuses to let you forget.

### Verifying a call demands asserting its outcome

A `Called` chain on its own is not enough. Whenever a subtest verifies any call,
`testigo` also requires **at least one result/state assertion** — an
`assert.Equal`, `NoError`, `Len`, `Contains`, `DidChange`, and so on. If the
verified method returns N values, it requires N of them. Otherwise the subtest
fails:

```
testigo: calls were verified, but only 0 result/state assertion(s) were made (1 required).
```

This stops "interaction-only" tests that prove *who was called* but never check
*what came out*. A test passes only when it pins both the interaction **and** the
outcome:

```go
msg, err := g.Welcome(u)

assert.NoError(t, err)                        // outcome
assert.Equal(t, msg, "Hello, Grace")          // outcome
assert.That(g).Called(mailer.Send).WithParams(u.Email) // interaction
```

When the outcome is stateful rather than returned, `assert.That(x).DidChange()`
(or an `Equal`/`Len` on the changed state) satisfies the requirement.

### The `Called` chain

Every check starts at `That(caller)` — it always states *who* called whom:

```go
assert.That(g).Called(mailer.Send)                  // exactly once (the default)
assert.That(g).Called(mailer.Send).Twice()          // exactly twice
assert.That(g).Called(mailer.Send).Times(7)         // exactly n times
assert.That(g).Called(mailer.Send).Never()          // not called at all
assert.That(g).Called(mailer.Send).AtLeastOnce()    // one or more times

assert.That(g).Called(mailer.Send).WithParams("ada@mail.test") // match the arguments

assert.Expect(t).That(g).Called(mailer.Send)        // explicit handle (your own goroutines)
```

Inside `testigo.Run` (or after `NewDouble`) no `testing.T` is needed — failures
land on the right subtest automatically. Arguments are **deep-copied at call
time**, so a slice/map/pointer the subject mutates *after* the call still matches
what the double actually received.

---

## Automatic restore between subtests — no `BeforeEach`/`AfterEach`

`NewDouble(t, x)` snapshots `x` at registration as an **immutable baseline**.
Before every `testigo.Run`, each registered double is restored to that baseline:

- in-memory state is **deep-copied back** from the baseline;
- any `Spy` the double holds is **cleared**, so recorded calls never leak;
- references to other registered doubles and `*Spy` pointers are kept by
  identity, so the wiring between doubles survives the restore.

So you mutate a double freely inside a subtest, and the next one starts pristine —
with no copy-on-write authoring and no teardown to write:

```go
type Inbox struct{ messages []string } // a plain double, no Spy

func (i *Inbox) Add(m string) { i.messages = append(i.messages, m) }

func TestRestore(t *testing.T) {
	inbox := testigo.NewDouble(t, &Inbox{})

	testigo.Run(t, "mutates the double freely", func(t *testing.T) {
		inbox.Add("one")
		inbox.Add("two")
		assert.Equal(t, len(inbox.messages), 2)
	})

	testigo.Run(t, "next subtest starts pristine", func(t *testing.T) {
		// deep-copied back from the immutable baseline — no teardown written.
		assert.Equal(t, len(inbox.messages), 0)
	})
}
```

### The immutability principle behind it

Shared mutable state between tests is the single biggest source of
order-dependent flakiness: one test leaves a value dirty, the next quietly
depends on it. The usual answer is a `BeforeEach`/`AfterEach` (or `setUp`/
`tearDown`) pair where **you** re-create or reset everything by hand — boilerplate
you have to *remember* to keep in sync as the doubles grow, and that an AI
assistant is especially prone to drop.

`testigo` removes those hooks entirely. The baseline captured at `NewDouble` is
treated as immutable; the **library**, not your test, guarantees every subtest
sees a fresh deep copy of it. You write each subtest as if state were immutable,
and the restore enforces it for you — there is nothing to forget.

```text
xUnit style                         testigo
-----------                         -------
BeforeEach: build/reset doubles  →  NewDouble(t, x)   // once, restored automatically
   the test                      →     the test       // mutate freely
AfterEach: tear everything down  →  (nothing)
```

### `Resetter` — doubles backed by external state

A deep copy can restore in-memory fields, but not state a struct copy can't reach:
a database, a temp dir, a file on disk. Such a double implements `Resetter` —
a single `Reset()` method — and `testigo` calls it at every reset point. It embeds
no `Spy`, so its live handles are never copied; only `Reset` runs.

```go
// External state: a temp dir. No Spy embedded — it restores via Reset.
type FileStore struct{ dir string }

func NewFileStore(t *testing.T) *FileStore { return &FileStore{dir: t.TempDir()} }

func (s *FileStore) Save(name, body string) error {
	return os.WriteFile(filepath.Join(s.dir, name), []byte(body), 0o600)
}

func (s *FileStore) Count() int { es, _ := os.ReadDir(s.dir); return len(es) }

// Reset wipes and re-seeds the external state; testigo runs it before each Run.
func (s *FileStore) Reset() {
	es, _ := os.ReadDir(s.dir)
	for _, e := range es {
		os.Remove(filepath.Join(s.dir, e.Name()))
	}
}

func TestFileStore(t *testing.T) {
	store := testigo.NewDouble(t, NewFileStore(t))

	testigo.Run(t, "first subtest writes one file", func(t *testing.T) {
		store.Save("a.txt", "hi")
		assert.Equal(t, store.Count(), 1)
	})

	testigo.Run(t, "second subtest starts empty again", func(t *testing.T) {
		assert.Equal(t, store.Count(), 0) // Reset already ran — no AfterEach
	})
}
```

Any double that embeds `Spy` already satisfies `Resetter` for free
(`Spy.Reset` clears recorded calls), so the common case needs nothing extra.

---

### Doubles & lifecycle (root package)

```go
mailer := testigo.NewDouble(t, &MailerSpy{}) // register + snapshot
testigo.Run(t, "name", func(t *testing.T) {   // restore before the subtest
	// ...
})
testigo.Reset(t)                              // restore on demand
spy := testigo.NewSpy()                       // standalone *Spy field
clone := testigo.Copy(value)                  // deep copy, shares no memory
```

### Fixtures — object mothers

```go
var users = fixture.New(User{Name: "Ada", Email: "ada@mail.test"})

var asGrace = func(u User) User { u.Name = "Grace"; return u }

grace := users.With(asGrace)                   // one variant
many := users.Times(3, randomName)             // 3 independent variants
maybeVIP := users.With(fixture.Maybe(asVIP))   // asVIP applied ~half the runs
either := users.With(fixture.OneOf(asGrace, asVIP)) // one of them, at random
```

### Random data, replayable

```go
// Seed must run before any random value is drawn (e.g. in TestMain).
testigo.Seed("river-stone-maple-frost") // fix the run from a readable phrase

name := random.Name()
email := random.Email()
when := random.AnyDateAfterNow()
pick := random.Pick("a", "b", "c")
```

### State changes & value asserts

```go
assert.That(mailer).DidChange()  // a non-Spy field differs from registration
assert.NoError(t, err)
assert.Equal(t, got, want)       // generic: cross-type compare is a compile error
```

---

## Docs

- **API reference:** [pkg.go.dev/github.com/lautaromei/testigo](https://pkg.go.dev/github.com/lautaromei/testigo)
  - [`/assert`](https://pkg.go.dev/github.com/lautaromei/testigo/assert) ·
    [`/fixture`](https://pkg.go.dev/github.com/lautaromei/testigo/fixture) ·
    [`/random`](https://pkg.go.dev/github.com/lautaromei/testigo/random)

## License

MIT — see [LICENSE](LICENSE).
