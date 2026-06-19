# testigo audit layer — design & implementation plan

A higher-abstraction analysis layer over the doubles testigo already records. It
turns the scattered, per-test checks testigo runs today into one **suite-level**
engine that surfaces the bug classes a test suite *fails to catch*, classifies
them with **Orthogonal Defect Classification (ODC)**, and is validated and
calibrated against real mutation testing using **MAE / R² under
Leave-One-Group-Out (LOGO)** cross-validation.

Scope for v1: **per package** (one test binary = one process). Cross-package
aggregation is a later, file-based merge step and is out of scope here.

> **Honest scope, stated up front (see §1.1 and §7).** testigo observes the
> **double boundary** — recorded calls, args, return/outcome classes, double
> state. So the shipped detectors are a surrogate for the subset of surviving
> mutants that are **observable at that boundary**. Bugs that live purely inside
> a function body and never change a recorded call/return/state are *invisible*
> to the recorded-metadata detectors. §5.E + §9 add a **checked-coverage** layer
> (statement-level, dev/CI) that reaches below the boundary to close part of
> that gap. The product is positioned accordingly: it finds **interaction /
> contract / state** gaps in your suite; pair it with the checked-coverage layer
> (and real mutation testing / property tests) for algorithmic correctness.

---

## 1. The one idea

Every heuristic answers a single question:

> **What code change would this test suite fail to catch?**

testigo already stores both halves of the answer each run:

- what the code *did* — `CallRecord{CallerComponent/Method, CalleeComponent/Method, CallSiteFile/Line, Params, Snapshots, Seq, Time}` (`internal/core/spy.go`), double-state baselines (`doubleRecord.initial`), interaction coverage (`observedMethods`/`assertedMethods`);
- what the test *pinned* — the assertion chains (`*CalledFunc{expectedArgs, times, atLeast, returnCount}`) and value-assertion counts (`testVerifier.valueAsserts`).

The **gap** between *did* and *pinned* is the space of changes no assertion
constrains. Each detector hunts one class of change living in that gap. A change
that no assertion would catch is — **provided the change is observable at the
double boundary** — a **surviving mutant**, so the audit layer is a fast,
single-run **surrogate for boundary-observable mutation testing** (see §7),
without recompiling or re-running anything.

### 1.1 The bound, and the prior art that names it

This "did minus pinned" idea is the interaction-layer cousin of **checked
coverage** (Schuler & Zeller, ICST 2011 / STVR 2013): the dynamic slice of
covered statements that actually influence an oracle. Statements covered but
outside every oracle's slice are exactly where mutants survive; checked coverage
was shown to be *more sensitive than mutation testing* and was motivated as a
cheaper proxy that sidesteps the equivalent-mutant problem.

The difference — and the bound — is the **sensor**:

| | checked coverage | testigo audit (shipped) |
|---|---|---|
| Granularity | statement (dynamic slice) | double boundary (recorded calls/args/outcomes/state) |
| Cost | instrumentation + slicing | reads metadata already recorded; zero extra runs |
| Sees | intra-function computation | only changes that cross a double or alter recorded state |
| Blind to | — | pure-logic bugs that never reach a recorded boundary |

So testigo's shipped detectors are a **bounded** surrogate: they catch the
interaction/contract/state slice of surviving mutants cheaply, and miss the
intra-function slice. §5.E integrates a statement-level checked-coverage
detector (heavier, dev/CI tier) to recover part of the intra-function slice, and
§9 uses checked coverage as a **second oracle** alongside mutation testing.

---

## 2. Architecture: accumulate per test, judge once at suite end

Coverage is a property of the **suite**, not of a test: test B may pin the branch
test A skipped. So detectors must not fire in a per-test cleanup — that produces
false positives. Instead:

1. **Accumulate (during each test).** A dedicated accumulator hook pushes a
   **compact digest** forward into a package-global accumulator. Only hashes /
   classes are stored — never live pointers — so memory stays flat across a
   large suite.

2. **Judge (once, at suite end).** `audit.Report()` runs every detector over the
   union of all tests. A gap fires only if **no test in the run closed it.**

### 2.1 The accumulation seam (corrected)

Two facts about the current code shape the seam, and the original draft of this
plan got both slightly wrong:

- **`finalCheck` does *not* hold the raw calls.** It has the expectation set
  (`tv.pendings → exps`) and `tv.valueAsserts` in hand, but it reaches the raw
  `[]*CallRecord` only *indirectly*, through `checkUncoveredCalls(exps...)` →
  `collectTestCalls()` (`spy.go:753`). So `auditAccumulate` must call
  `collectTestCalls()` itself (one extra traversal of the test's spies).

- **Value-only tests never arm `finalCheck`.** `armFinalCheck` is only reached
  from `registerForFinalCheck`, which is only called inside `.Called(...)`. A
  test that uses only `assert.Equal`/`DidChange` and never starts an `Expect`
  chain registers no cleanup, and `noteValueAssertion` uses `Load` (not
  `LoadOrStore`), so its `valueAsserts` is dropped. Piggy-backing *solely* on
  `finalCheck` would silently skip those tests.

**Fix — arm at first touch.** Introduce `auditArm(t)`, called the first time a
test touches testigo (from `NewDouble` *and* from `Expect`/`Called` *and* from
the first value assertion), registering **one** accumulator cleanup per test.
Because `NewDouble` runs before any assertion and `t.Cleanup` is LIFO, that
cleanup is registered early and therefore runs **last among testigo's own
cleanups** — i.e. while `doubleRecords` and the spies are still live. That
ordering is **load-bearing** for the state/alias/goroutine detectors (12, 13,
14) and is asserted by a dedicated test in Phase 0 (§11).

`finalCheck` still contributes the assertion-derived signal it already computes;
`auditAccumulate(t, exps, calls, valueAsserts)` runs from the first-touch
cleanup so interaction-only and value-only tests both accumulate.

### 2.2 Two signal lifetimes, kept separate

The plan deliberately distinguishes:

- **Global, persistent signal** — `observedMethods` / `assertedMethods`
  (`coverage.go`), updated on *every* spied call and *every* `newExpectation`,
  independent of `finalCheck`, surviving until `ResetCoverage`. The Coverage
  family (1, 4, 5, 6, 16, 18) reads this directly and is therefore robust to the
  value-only-test gap above.
- **Per-test, ephemeral signal** — raw `CallRecord`s, arg multisets, seq pairs,
  double-state snapshots, `valueAsserts`. Captured at the first-touch cleanup,
  hashed into the accumulator, then torn down with the test.

Mixing these two was a latent bug in the original draft; keeping them explicit is
the fix.

### Hook = `TestMain`

```go
func TestMain(m *testing.M) {
    testigo.Seed("river-stone-maple-frost")
    code := m.Run()
    audit.Report()        // detectors run here, over the union of all tests
    os.Exit(code)
}
// or: audit.Main(m) wraps run + report + exit
```

There is no `*testing.T` at suite end, so **severity is an exit code**:

| `TESTIGO_AUDIT` | Behavior |
|---|---|
| `off` | accumulate nothing, report nothing |
| `warn` (default) | print the ODC roll-up; exit code unchanged |
| `error` | print, and `os.Exit(1)` if any finding ≥ its threshold |

`audit.AsErrors()` / `audit.Disable(rule)` provide the same control in code.
`audit.Ignore(rule, site)` (§8.1) suppresses a single finding for baselining.

---

## 3. Package layout

```
audit/audit.go                       // thin public API: Report(), Main(m), AsErrors(), Disable(rule), Ignore(rule, site)
internal/core/audit.go               // accumulator, detector iface, registry, finding (scored|hazard) + AI_FIX renderer, severity, ODC tag, per-suite estimator
internal/core/audit_coverage.go      // detectors triggered by Coverage   (family A)
internal/core/audit_variation.go     // detectors triggered by Variation  (family B)
internal/core/audit_sequencing.go    // detectors triggered by Sequencing (family C)
internal/core/audit_interaction.go   // detectors triggered by Interaction(family D)
internal/core/audit_checked.go       // family E: statement-level checked-coverage detector (go/ssa + coverprofile; dev/CI tier)
internal/core/audit_odc.go           // odcClass(rule) -> ODC lookup + distribution renderer
internal/core/audit_test.go          // per-detector unit tests over a hand-built accumulator
internal/core/testdata/auditfixture/ // a tiny package run end-to-end for one golden report
cmd/audit-eval/                      // offline: mutation + checked-coverage oracle, metrics, LOGO, calibration (dev/CI only)
benchmark/<pkgs>/                    // labeled corpus for eval, tagged by boundary-observability (§9)
internal/core/audit_calibration.go   // GENERATED: fitted, shipped calibration constants
```

The interaction detector files are grouped by **ODC Trigger**, because (see §6)
the trigger axis is the natural family grouping. Family E (checked coverage) is
**not** an ODC trigger — it is a complementary statement-level adequacy
criterion; its findings are still ODC-*typed* but are not triggered by the four
function-test triggers.

### 3.1 Two naming notes

- **`mutation` is overloaded in this repo.** `internal/core/mutations.go`
  already exists and is about *state mutation of doubles* (`doubleRecord`,
  change detection) — nothing to do with source mutants. To avoid two meanings
  of "mutation" in one package, this plan uses **source-mutant** for the eval's
  injected faults and **state-mutation** for double changes, and folds the
  existing `mutations.go` content into `doubles.go` as part of Phase 0.
- **Don't rebuild the mutator.** `cmd/audit-eval`'s source-mutant generator is a
  thin wrapper over an existing Go mutation tool (Gremlins or go-mutesting —
  both already do `go/ast` relational/arith/arg/drop/reorder mutation with
  timeout handling), adding only (a) the ODC tag per operator and (b) the
  boundary-observability tag (§9). Building a bespoke mutator from scratch buys
  nothing and re-solves timeout/compile handling those tools already got right.

---

## 4. Core abstractions

```go
// ODC tags. Static per rule; orthogonal axes. NOTE: this is a designer's
// ODC-aligned labeling of detectors, not emergent defect classification (§6).
type ODC struct {
    Type      string // Assignment | Checking | Algorithm | Timing/Serialization | Interface | Function | Relationship
    Trigger   string // Coverage | Variation | Sequencing | Interaction | Statement(checked-coverage; not an ODC trigger)
    Qualifier string // Missing | Incorrect | Extraneous
    Impact    string // Reliability | Capability | Performance | Security | Maintainability
}

type findingKind int
const (
    scored findingKind = iota // predicts a surviving source-mutant; has a validating operator; enters MAE/R²
    hazard                    // single-run truth / smell; reported, NOT scored, excluded from the eval (§9)
)

// A single risk. score in [0,1] is the prediction (for MAE/R²); threshold only
// decides what prints. fix carries the existing AI_FIX block.
type scoredFinding struct {
    rule       string
    kind       findingKind // scored | hazard
    odc        ODC
    score      float64
    observable bool   // boundary-observable? false for family E statement findings
    site       string // file:line
    message    string
    fix        aiFix
}

// What every test contributes; the union of these is all a detector sees.
type acc struct {
    methods    map[string]*methodStat   // observed/asserted/exact-counted/loose-only per callee method
    outcomes   map[string]map[uint64]*outcomeStat // method -> outcome-class hash -> observed/asserted
    args       map[string]map[int]*argStat        // method -> argIndex -> pinned? value multiset, numeric range, literal-set
    emits      []emitEdge               // caller->callee with downstream/effect-asserted flags
    orderPairs *pairSet                 // interleaved pairs seen; pairs that were order-asserted
    doubles    []doubleShape            // structural snapshot for alias / external-state scans
    goroutines map[uint64][]callRef     // per-test owning goroutine -> calls, for detector 13 (needs CallRecord.GoroutineID, §10 Phase 0)
    covered    *coverProfile            // family E: covered blocks from `go test -coverprofile` (dev/CI tier; nil otherwise)
    findings   []scoredFinding          // single-run truths stashed mid-test (overwrite, aliasing, ...)
    mu         sync.Mutex
}

type detector interface {
    name() string
    odc()  ODC
    kind() findingKind
    inspect(*acc) []scoredFinding
}

// PerSuiteRisk is the single number compared against the oracle's mutation
// score (§9). It is the mean predicted P(survive) over the *same* candidate
// sites the oracle enumerates, restricted to boundary-observable sites.
type PerSuiteRisk struct {
    PredictedSurvivalRate float64 // mean P(survive) over observable candidate sites
    ObservableFraction    float64 // |observable sites| / |all sites| — how much the sensor can see
}
```

A registry runs every detector at suite end, dedups, calibrates each raw score
(§9), renders the ODC distribution (§8) and the per-finding `AI_FIX` blocks, then
applies severity. Rendering reuses the existing `reportWarning` / `reportFinal`
split and the `AI_FIX:` format already emitted by `unexpectedCallsFix` and friends.

**Substrate gap to fill (Phase 0):** detector 13 needs the owning goroutine at
*call* time. `CallRecord` records `Seq` and `Time` but **no goroutine id**, so a
`GoroutineID uint64` field is added to `CallRecord` and populated in the spy
record path. (`bindGoroutine`/`goroutineTests`/`getTestID` already exist for
test↔goroutine binding, but the per-call id is new.)

---

## 5. The detectors — detection

18 interaction/boundary detectors grouped by ODC Trigger (= family A–D), plus 1
statement-level checked-coverage detector (family E). Each is tagged **scored**
(predicts a surviving source-mutant, has a validating operator in §9, enters
MAE/R²) or **hazard** (single-run truth or smell; reported but *not* scored,
because no source-mutant operator corresponds to it). "Cancelled when" is the
suite-global rule that makes a gap *not* fire because some other test closed it;
"—" marks a single-run truth no other test can cancel.

### A. Coverage trigger — "did the suite execute & assert this path?"

| # | Heuristic | Kind | Validated by | Reads | Cancelled when |
|---|---|---|---|---|---|
| 1 | outcome-under-cover¹ | scored | branch-removal | observed vs asserted outcome-classes / method | some test asserts that class |
| 4 | loose-count | scored | dup-call | `exp.atLeast` per method | some test exact-counts it |
| 5 | outcome-unpinned | scored | **return-corruption** | `returnCount` vs `valueAsserts` for a verified method | some test asserts that outcome |
| 6 | never-asserted-method | scored | drop-call | `observedMethods` vs `assertedMethods` | some test asserts it |
| 15 | tautology | hazard | — (static smell) | `sourceLines`: got-expr == want-expr (`Equal(true,true)`) | — (always a smell) |
| 16 | discarded-return² | scored | return-corruption | `assignmentIgnoresReturnedCall` (exists), aggregated | some test asserts that outcome elsewhere |
| 18 | error-path-unexercised | scored | **force-error** | method whose error-return was never observed ≠ nil | some test observes & asserts the error path |

¹ **Renamed from "branch-under-cover".** testigo has no statement/branch
instrumentation; it sees method-level observed/asserted + outcome classes, not
branches. The honest name is *outcome-under-cover*. True branch reach comes from
family E (`-coverprofile`), not this detector.
² Detector 16 is the existing `ignoredReturnedValues` check, aggregated to suite
level — a re-home, not a new mechanism. Detectors 1 and 5 share the
outcome-class mechanism and are calibrated as one feature family (§9).

### B. Variation trigger — "did the suite vary the inputs?"

| # | Heuristic | Kind | Validated by | Reads | Cancelled when |
|---|---|---|---|---|---|
| 2 | unpinned-arg | scored | arg-corruption | `expectedArgs` vs `recorded()` per (method, argIdx) | some test pins that arg concretely |
| 3 | boundary-blind | scored | relational-flip | union of numeric arg values per (method, argIdx) | union straddles the boundary |
| 20 | argument-swap-blind | scored | **arg-swap** | ≥2 same-typed args on a call never pinned to mutually distinct values | some test pins two same-typed positions to distinct values |

> **Calibration verdicts (eval on `testigo-usage`).** Detector **17
> literal-pinned-once was removed** — under testigo's strict runtime a pinned arg
> always catches any value change, so it predicted impossible survivors (prec
> 0.00). Detector **2 unpinned-arg ships OFF by default** (`auditExperimentalRules`;
> prec ~0.43 — over-fires on args constrained indirectly via an asserted outcome);
> enable with `TESTIGO_AUDIT_EXPERIMENTAL=on`. Validated & shipped on:
> argument-swap-blind (1.00), loose-count (1.00), boundary-blind, order-insensitive.

### C. Sequencing trigger — "did the suite exercise order?"

| # | Heuristic | Kind | Validated by | Reads | Cancelled when |
|---|---|---|---|---|---|
| 7 | order-insensitive | scored | reorder | `Seq` + whether any order-assert ran for the pair | some test `Before/After/CallsOrdered` the pair |
| 8 | late-async-call | hazard | — (timing hazard) | call goroutine + assert/test-end `Time` | some test asserts after the async settles |
| 10 | overwrite / dead-store | scored | dead-store-delete | ordered writes per (component, key); reads between | **some test reads that key after the write** |
| 14 | unrestored-external-state | hazard | — (state leak) | `Resetter` double whose observable state ≠ baseline after `Reset` | — (real bug) |

> **Detector 10 downgraded.** The original draft marked it "real bug — never
> cancelled". But a single test seeing write→write without a read in between
> does **not** mean no test (or production code) reads that key — that violates
> the very "coverage is a suite property" principle in §2. It is now **scored**
> (it predicts a surviving dead-store-delete mutant) and **cancellable** when
> any test reads the key.

### D. Interaction trigger — "did the suite make components interact?"

| # | Heuristic | Kind | Validated by | Reads | Cancelled when |
|---|---|---|---|---|---|
| 9 | event-drop | scored | drop-emit | emit-verb call; downstream callee; effect asserts | some test asserts the emit's effect |
| 11 | arg-aliasing | hazard | — (real bug) | `Params` vs `Snapshots` (reuses `aliasingWarning`) | — (real bug) |
| 12 | shared-backing-memory | hazard | — (real bug) | `doubleRecords` + reflect: two doubles, same map/slice/ptr | — (dedup only) |
| 13 | cross-goroutine-mutation | hazard | — (data race) | double mutated off its owning-test goroutine (`CallRecord.GoroutineID`) | — (real hazard) |

### E. Statement layer — checked coverage (dev/CI tier, ODC-orthogonal)

| # | Heuristic | Kind | Validated by | Reads | Cancelled when |
|---|---|---|---|---|---|
| 19 | unchecked-statement | scored | any-covered-statement-mutation | covered blocks (`-coverprofile`) ∩ NOT in any asserted return's static slice (`go/ssa`) | some test's assertion slice includes it |

Detector 19 is the integration of checked coverage (see §5.1). It reaches
**below** the double boundary, so `observable=false` and it is evaluated against
the **checked-coverage oracle**, not the boundary-mutation oracle (§9). It is
gated behind a build tag / `TESTIGO_AUDIT_CHECKED=on` because it needs SSA +
coverage and is heavier than the recorded-metadata detectors.

### 5.1 How checked coverage is approximated in Go (detector 19)

Real checked coverage needs a *dynamic* backward slice from each oracle. A full
dynamic slicer is out of scope for v1, so detector 19 uses a **static,
SSA-based** approximation that is cheap and dependency-light:

1. **Covered set.** Run the suite once with `go test -coverprofile`. This yields,
   per package, the basic blocks actually executed by the suite (built-in, no
   instrumentation we write).
2. **Checked set (approximate).** For each function whose **return value is
   asserted by the suite** (testigo already tracks asserted methods/outcomes),
   compute the *static backward slice* of that return over the function's
   `golang.org/x/tools/go/ssa` form — the def-use closure of SSA values the
   return data/control-depends on. The union of those slices over all asserted
   returns is the approximate "checked" set.
3. **Gap = covered − checked.** A covered block outside every slice is a
   statement the suite *runs but never lets influence an oracle* — exactly where
   a mutant survives, at statement granularity the boundary detectors can't see.

This is an over-approximation of "checked" (static slices include more than a
dynamic slice would), so it is *conservative about firing* — it under-reports
gaps rather than crying wolf, which is the right bias for a lint-like tool. It is
validated in §9 against the checked-coverage oracle (which can compute the real
dynamic slice offline).

### Detection notes

- **Outcome class (1, 5)** — the fuzziest knob. v1 default bucket:
  `(error-vs-nil result) × (hash of arg-class per pointer/enum/bool arg)`. Tunable,
  and tuned by the eval layer (§9). Start loose, tighten with data.
- **Verbs** — emit verbs (`Publish|Emit|Fire|Dispatch|Notify|Send|On|Handle`),
  write verbs (`Set|Insert|Update|Write|Store|Put|Save`), read verbs
  (`Get|Query|Read|Load|Find|List`). Configurable sets; matched on `CalleeMethod`.
- **Key (10)** — `recorded()[0]` of a write call is the row key; a write→write on
  the same `(CalleeComponent, key)` with no read of that key in between (by `Seq`)
  predicts a surviving dead-store-delete *this run*; cancelled if any test reads
  the key. `memdb` is the canonical showcase.
- **Hazards (8, 11, 12, 13, 14, 15)** — detected during `auditAccumulate` and
  stashed into `acc.findings`; they surface in the same suite-end roll-up but
  are **not scored** and are **excluded from the §9 MAE/R²** because no
  source-mutant operator corresponds to them. They are reported under a separate
  "hazards" heading (§8).

---

## 6. ODC classification

The four ODC **function-test triggers** — Coverage, Variation, Sequencing,
Interaction — *are* families A–D above; for those, ODC is not bolted on, it is
the correct grouping, because "which fix kills this mutant" is precisely an ODC
Defect Type. **Honest caveat:** this is a *static, designer's* labeling of
detectors onto ODC cells, not emergent classification of observed defects (ODC's
original use was to classify real defects discovered over a lifecycle and compare
the distribution against an expected profile). The trigger correspondence is
genuine; the Type/Qualifier/Impact assignment is ours.

| # | Heuristic | Defect Type | Trigger | Qualifier | Impact |
|---|---|---|---|---|---|
| 1 | outcome-under-cover | Checking | Coverage | Missing | Reliability |
| 2 | unpinned-arg | Interface | Variation | Missing | Capability |
| 3 | boundary-blind | Checking | Variation | Missing | Reliability |
| 4 | loose-count | Algorithm | Coverage | Missing | Reliability |
| 5 | outcome-unpinned | Assignment | Coverage | Missing | Capability |
| 6 | never-asserted-method | Function | Coverage | Missing | Capability |
| 7 | order-insensitive | Timing/Serialization | Sequencing | Missing | Reliability |
| 8 | late-async-call | Timing/Serialization | Sequencing | Incorrect | Reliability |
| 9 | event-drop | Interface | Interaction | Missing | Capability |
| 10 | overwrite / dead-store | Assignment | Sequencing | Extraneous | Performance |
| 11 | arg-aliasing | Interface | Interaction | Incorrect | Reliability |
| 12 | shared-backing-memory | Relationship | Interaction | Incorrect | Reliability |
| 13 | cross-goroutine-mutation | Timing/Serialization | Interaction | Incorrect | Security |
| 14 | unrestored-external-state | Assignment | Sequencing | Missing | Reliability |
| 15 | tautology | Checking | Coverage | Incorrect | Maintainability |
| 16 | discarded-return | Checking | Coverage | Missing | Capability |
| 18 | error-path-unexercised | Checking | Coverage | Missing | Reliability |
| 19 | unchecked-statement | Checking | Statement* | Missing | Reliability |
| 20 | argument-swap-blind | Interface | Variation | Incorrect | Reliability |

\* Statement is **not** an ODC function-test trigger; it marks a
checked-coverage finding, which is ODC-orthogonal.

**Orthogonality, measured honestly.** Types span 7 of the 8 canonical ODC types
(only Build/Package/Merge is absent — correct, since this is a unit-test layer).
But the catalog does **not** tile the space uniformly, and the original draft
overstated this: by Qualifier it is **Missing-heavy** (12/19) with Extraneous
appearing once; by Impact it is **Reliability-heavy** (≈11/19). That skew is
*expected and correct* — coverage gaps are, by nature, *missing* checks that
threaten *reliability* — but it should be reported as a known shape, not sold as
an even tiling. The Trigger and Type axes are the well-spread ones.

### Output: a diagnosis, not a list

`audit.Report()` aggregates the **scored** findings into an ODC distribution,
sorted by Impact, then lists **hazards** separately:

```
testigo audit — ODC profile of surviving mutants (package: ./pricing)
  observable fraction: 0.71  (29% of candidate mutants live below the double boundary — see checked-coverage layer)
  by Trigger
    Variation    ████████░░  41%   suite rarely varies inputs / hits boundaries
    Sequencing   ██████░░░░  29%
    Coverage     ███░░░░░░░  18%
    Interaction  ██░░░░░░░░  12%
  dominant cell: Checking / Variation / Missing  →  add boundary cases

  hazards (not scored): 1 data race (cross-goroutine-mutation), 1 tautology — see below

  AI_FIX:
  problem: boundary_blind
  rule: boundary-blind  kind: scored  odc: Checking/Variation/Missing  score: 0.82
  site: pricing_test.go:74
  evidence: Cart.Total arg #1 only ever observed as {30}; a <=/< flip survives
  suggested_fix: add a case at the boundary value and assert the outcome
```

One line — "41% Variation-triggered" — names the suite's *systematic* blind spot;
"observable fraction: 0.71" names how much of the mutant space the sensor can
even see.

---

## 7. Intelligent mutation: two oracles, teacher / student

The shipped library never mutates source and never re-runs. It predicts surviving
mutants from a single recorded run. To *validate and calibrate* those predictions
we use, **offline, at dev/CI time only**, **two** ground-truth oracles — neither
enters the runtime path:

- **Teacher 1 — mutation testing** (slow, exact, but limited by operators and by
  equivalent mutants): mutate source → run suite → observe survived / killed.
  Validates the **scored, boundary-observable** detectors.
- **Teacher 2 — checked coverage** (cheaper, statement-level, *no* equivalent
  mutants): the dynamic slice of covered statements that influence an oracle;
  covered-but-unchecked statements are the ground truth for detector 19 and a
  second, independent check on the boundary detectors. This is the integration
  Schuler & Zeller motivate: a sensitive, equivalent-mutant-free adequacy signal.
- **Student** = the detectors (single-run, cheap, shipped).
- **Fidelity** = how well the student copies each teacher, measured by §9.

"Intelligent, not a mutation lib" means: mutation testing and checked coverage
are the *measuring sticks*, not the product. (We avoid the word "distillation":
this is supervised calibration against the oracle labels, not output-distribution
matching.)

**The bound, restated for the eval.** Because the student's sensor is the double
boundary, it *cannot* predict mutants that never reach the boundary. The eval
must therefore measure the student only where it can see (boundary-observable
mutants, Teacher 1) and use Teacher 2 for the statement layer — otherwise the
headline number is an artifact of corpus composition (§9.4).

---

## 8. Each finding carries its AI_FIX

Unchanged in spirit from testigo's current output: every finding renders an
`AI_FIX:` block with `problem`, `evidence`, `suggested_fix`, now plus `rule`,
`kind` (scored|hazard), `odc`, and `score`. This keeps the output
machine-actionable for an agent fixing the suite, and consistent with
`unexpectedCallsFix` / `ignoredReturnedValuesFixMessage` /
`outcomeAssertionFixMessage` already in `internal/core`.

### 8.1 False-positive budget

A lint-like tool dies on false positives, so the product (not just the eval) gets
suppression from day one: `audit.Ignore(rule, site)` and a checked-in
`.testigo-audit-ignore` baseline file (rule + site + reason). The eval enforces a
**precision floor** per detector (§9); any detector that can't clear it ships
**off by default**.

---

## 9. Eval & calibration ("work like a fitted model")

### Make heuristics measurable

Scored detectors emit a continuous `score ∈ [0,1]` (the prediction), not a bool.
The threshold only gates printing. Hazards are not scored and are excluded from
every metric below.

### Ground truth = two offline oracles

`cmd/audit-eval` over `benchmark/`:

1. **Source-mutant oracle.** A wrapper over Gremlins / go-mutesting (§3.1)
   generates exactly the classes the scored detectors target — each tagged with
   (a) its ODC class and (b) its **boundary-observability**:

   | operator | validates detectors |
   |---|---|
   | relational-flip | 3 |
   | arg-corruption | 2 |
   | return-corruption **(added)** | 5, 16 |
   | value-mutation **(added)** | 17 |
   | arg-swap **(added)** | 20 |
   | drop-call / drop-emit | 6, 9 |
   | reorder | 7 |
   | dup-call | 4 |
   | dead-store-delete | 10 |
   | branch-removal | 1 |
   | force-error **(added)** | 18 |
   | any-covered-statement | 19 (checked-coverage oracle) |

   The original draft's operator list omitted **return-corruption**, leaving
   detectors 5 and 16 unvalidatable despite §5 listing them as corrupt-return
   hunters; it is added here, along with value-mutation and force-error for the
   new detectors 17/18. **Only the scored detectors with a matching operator are
   validated; the six hazards are not** — and the report says so rather than
   implying the whole catalog is fitted.

2. **Checked-coverage oracle.** Compute the real dynamic checked coverage offline
   (instrumented run); covered-but-unchecked statements are the labels for
   detector 19 and an independent reach check. Cheaper than mutation and free of
   equivalent mutants.

3. **Equivalent-mutant handling.** Survivors can be *equivalent* (unkillable),
   not test gaps — undecidable in general, and 4–39% of mutants in practice. Left
   unhandled they bias every "survived" label toward "gap" and put a floor on
   achievable MAE. Mitigations, applied in order: (a) prefer operators with low
   empirical equivalence rates; (b) filter with **Trivial Compiler Equivalence**
   (mutants the Go compiler optimizes to identical code are equivalent); (c)
   report the residual equivalent-rate estimate as the **label-noise floor** so
   MAE is read against it, not against 0.

Every kept mutant site now has an aligned
`(score_predicted, y_actual, odc_class, observable)`.

### Metrics — two granularities, ODC- and observability-aware

| Granularity | Prediction vs truth | Metrics |
|---|---|---|
| Per-suite (regression) | `PerSuiteRisk.PredictedSurvivalRate` vs actual **mutation score**, on observable sites | **MAE, R²** |
| Per-mutant (calibration) | score vs survived/killed | Brier, ROC-AUC, precision/recall/F1 |
| Per-ODC-class | both, sliced by Defect Type | per-class MAE/R²; confusion matrix; macro-F1 |
| Per-observability | recall on observable vs non-observable mutants, separately | recall@observable, recall@non-observable |

Brier / AUC stop a model from gaming MAE by predicting ≈0 everywhere (survival is
the minority class, so a constant-0 predictor scores deceptively low MAE); R² and
a precision floor guard the same. The per-ODC confusion matrix validates the
**classification**, not just the magnitude. The **per-observability** row is the
one the original draft lacked and is the most important: it separates "how good
is the predictor where it *can* see" (recall@observable, the honest headline)
from "how much of the mutant space it can see at all" (observable fraction). Read
without it, MAE/R² just reflect how mock-heavy the corpus is.

### 9.1 The per-suite estimator (defined)

`PerSuiteRisk.PredictedSurvivalRate` = mean predicted `P(survive)` over the
candidate mutation sites the oracle enumerates, **restricted to
boundary-observable sites** and enumerated by the **same** traversal the mutator
uses (so the two universes match). `ObservableFraction` = |observable sites| /
|all sites|. The headline MAE compares `1 − PredictedSurvivalRate` against the
oracle's mutation score on the observable subset. Non-observable sites are
reported via `ObservableFraction` and handed to the checked-coverage layer, not
silently scored as misses.

### 9.2 LOGO cross-validation

Group = package / project. Calibrate on K−1 groups, evaluate on the held-out one,
rotate; report mean ± std of MAE / R². Folds are **stratified by ODC class and by
observability** so each carries every class and both observability strata.
**Corpus floor:** LOGO with few groups gives high-variance per-suite R²; the
benchmark must hold **≥ 10 packages** spanning mock-heavy and pure-logic styles,
and per-ODC-class slices with < 30 mutants in a fold are reported as
"insufficient" rather than as a point estimate. This prevents thresholds
overfitting one codebase and stops rare classes (e.g. Security) producing
meaningless F1.

### 9.3 Calibration = ship a fitted model

Each scored detector emits raw features (e.g. #outcome-classes observed, #pinned,
arg value spread, literal-set size). Offline, fit a small **logistic-regression**
map features → P(survive) on the training folds (minimizing Brier ≈ MAE);
isotonic only where a fold has enough points to avoid overfitting. Fitted
coefficients are written to the **generated** `internal/core/audit_calibration.go`
and shipped as constants. Runtime stays dependency-free, deterministic, no LLM, no
recompile — but thresholds are fitted and LOGO-validated rather than guessed.

### 9.4 Validity ceiling (stated, not hidden)

The chain is heuristics → mutation score → real faults. Just et al. (FSE 2014)
show mutant detection correlates significantly with real-fault detection
independent of coverage — but ~27% of real faults coupled to *no* mutant. So the
upper bound on "does killing our predicted mutants catch real bugs" is set by
that coupling (~73%), and the corpus composition sets how much of the space is
observable at all. The eval reports both rather than implying MAE/R² measure
real-fault detection directly.

### CI gate

CI runs `audit-eval` and **fails if cross-validated MAE rises above budget** (or
R² drops below floor, or any shipped detector falls under its precision floor) —
all computed on the boundary-observable subset, with the checked-coverage oracle
gating detector 19 separately.

---

## 10. Build phases

| Phase | Deliverable |
|---|---|
| 0 | Scaffolding: `acc`, `detector` (with `kind()`), `scoredFinding`+ODC+observable, registry, AI_FIX renderer, env severity, `audit.Report()`/`Main(m)`, `Ignore`/baseline. **Seam:** `auditArm(t)` first-touch cleanup + `auditAccumulate`; assert the LIFO cleanup-ordering invariant (§2.1) in a test. **Substrate:** add `CallRecord.GoroutineID`; fold `mutations.go` into `doubles.go`. Smoke test only. |
| 1 | Family A (Coverage): 1, 4, 5, 6, 15, 16, 18 — reuse `observed/assertedMethods` (global signal) + expectations. Outcome classes first. |
| 2 | Single-run truths / hazards: 10 (scored, cancellable), 11, 12 — provable, near-zero false positives; 11 reuses `aliasingWarning`. |
| 3 | Variation + Sequencing + Interaction edges: 2, 3, 7, 9, 17 — graph + seq + arg multisets/literal-sets. |
| 4 | Remaining state/timing hazards: 8, 13, 14 (13 uses the new `GoroutineID`). |
| 5 | Family E (checked coverage): detector 19 — `go test -coverprofile` ingest + `go/ssa` static return-slice; gated behind `TESTIGO_AUDIT_CHECKED`. |
| 6 | Report polish: ODC distribution + observable-fraction + hazards section + README + a `memdb` demo (overwrite + shared-resource showcase). |
| 7 | Eval & calibration: dual oracle (mutation via Gremlins/go-mutesting wrapper **+** checked coverage), operator map incl. return-corruption/value-mutation/force-error, equivalent-mutant filtering (TCE), metrics (MAE/R²/Brier/AUC/F1 + per-ODC + per-observability), LOGO CV with corpus floor, fitted calibration constants, CI MAE-budget gate. |

ODC tagging is cross-cutting, not its own phase: the tag lands on `scoredFinding`
in Phase 0 and each detector declares its class as it is built.

---

## 11. Testing strategy

- **Per-detector unit tests** over a hand-built `acc` — no real test run needed;
  fast and deterministic. Assert both the firing condition and the suite-global
  cancel rule (build an `acc` where another "test" closes the gap; expect no
  finding). For hazards, assert they are emitted with `kind == hazard` and are
  excluded from any scored aggregate.
- **Cleanup-ordering invariant test** (Phase 0): a test that registers a double
  then asserts, and verifies the accumulator cleanup runs while `doubleRecords`
  and spies are still live (the LIFO assumption of §2.1).
- **One end-to-end**: `internal/core/testdata/auditfixture/` is a small package
  with deliberately weak and strong tests, plus a value-only test and an
  interaction-only test (to exercise the first-touch seam); run via `go test`
  and capture the `audit.Report()` roll-up as a golden file.
- Reuse the existing `fakeT` and `isolateCurrentTestRegistries()` helpers for
  registry isolation (already used in `calls_test.go`).
- **Eval is itself tested**: the oracle's MAE/R²/LOGO math gets unit tests on
  synthetic `(score, label)` sets with known answers; the operator→detector map
  and observability tagging get a fixture test; the checked-coverage slicer is
  tested against hand-verified small functions.

---

## 12. Decisions

Locked:

- Scope = **per package** for v1.
- Run mode = **auto-accumulate + suite-end report**, severity by exit code,
  `TESTIGO_AUDIT=off|warn|error` (default `warn`); first-touch accumulation seam.
- Findings are **scored** (predict a boundary-observable source-mutant, enter
  MAE/R²) or **hazard** (reported, excluded from the eval). The split is locked.
- Mutation testing is **offline teacher only**, never in the runtime path, and is
  a thin wrapper over an existing Go mutator (Gremlins / go-mutesting), not a
  bespoke one.
- **Checked coverage** is integrated as (a) a dev/CI statement-level detector
  (19) and (b) a second offline oracle. The shipped surrogate is explicitly
  **bounded to boundary-observable mutants**; the eval reports observable
  fraction and per-observability recall.
- Classification = **ODC** (Type / Trigger / Qualifier / Impact), as a static
  designer labeling, not emergent classification.
- Validation = **MAE / R² (+ Brier, AUC, macro-F1) under LOGO CV**, on the
  observable subset, with equivalent-mutant filtering (TCE), a fitted calibration
  shipped as generated constants, and a CI MAE-budget gate.

Open (cheap to revisit):

- Package name `audit` (vs `risk` / `lint`).
- Outcome-class bucketing for detectors 1 and 5 — start with the default in §5 and
  let the eval layer tune it.
- The verb sets in §5 — ship sensible defaults, allow override.
- Whether detector 19's static slice should later be upgraded to a dynamic slicer
  if the static over-approximation under-reports too aggressively.

---

## 13. Prior art & references

- **Checked coverage** — Schuler & Zeller, *Assessing Oracle Quality with Checked
  Coverage*, ICST 2011; *Checked coverage: an indicator for oracle quality*, STVR
  2013. The statement-level, slicing-based formalization of "did minus pinned",
  shown more sensitive than mutation testing and free of equivalent mutants.
- **Mutants vs real faults** — Just, Jalali, Inozemtseva, Ernst, Holmes, Fraser,
  *Are Mutants a Valid Substitute for Real Faults in Software Testing?*, FSE 2014.
  Significant coupling, but ~27% of real faults uncoupled — the validity ceiling.
- **Equivalent-mutant problem** — undecidable; 4–39% empirical rate; motivates the
  TCE filter (Trivial Compiler Equivalence) and reporting a label-noise floor.
- **Go mutation tools** — Gremlins (`go-gremlins/gremlins`, PITest-inspired) and
  go-mutesting (`avito-tech/go-mutesting`): the source-mutant teacher is a wrapper
  over these, adding ODC + observability tags.
- **ODC** — Chillarege et al. (IBM); function-test triggers Coverage / Variation /
  Sequencing / Interaction; 8 canonical defect types (we use 7, omitting
  Build/Package/Merge).
