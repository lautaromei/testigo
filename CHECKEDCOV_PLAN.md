# Plan — `checkedcov`: checked-coverage como CLI standalone para tests nativos + testify

## 0. Objetivo

Extraer la lógica de checked-coverage que hoy vive en `internal/checkedcovssa`
a una **herramienta CLI independiente** que corra sobre **cualquier paquete Go**
—no solo los que usan los doubles de testigo— reconociendo los tres idiomas de
aserción reales del ecosistema:

1. **nativo** `testing`: `if got != want { t.Errorf(...) }`, `t.Fatal`, `t.Fail`, …
2. **testify**: `assert.Equal(t, want, got)`, `require.NoError(t, err)`, …
3. **testigo**: `assert.That(...).Called(...)`, `assert.Equal(...)` (ya soportado)

La salida es una señal precisa —"este statement se ejecuta pero no alimenta
ninguna aserción"— pensada como **input para una IA que escribe tests**, para
que detecte casos/ramas no verificados en el código que produce.

### Por qué (señal sobre la que nos paramos)

`go test -cover` dice "esta línea se ejecutó". No dice "esta línea fue
*verificada*". Una suite puede tener 100% de cobertura y 0 asserts útiles. La
literatura (Schuler & Zeller 2011, *checked coverage*; Just et al. FSE'14,
mutación como sustituto de fallos) y los LLM-test-gen guiados por cobertura
(Qodo Cover-Agent) muestran que **line coverage es señal débil**: arxiv
2412.14137 demuestra que tests-LLM guiados por cobertura no encuentran bugs.
`checkedcov` da la señal fina que falta y que ninguna lib Go mainstream provee.

---

## 1. Qué existe hoy (base de la que partimos)

`internal/checkedcovssa/checkedcovssa.go` — `Run(dir string) error`:

1. **Covered set.** Corre `go test -covermode=set -coverprofile` en `dir`,
   parsea el perfil → `map[base]map[line]bool` de líneas ejecutadas.
2. **Checked set.** Construye SSA (`golang.org/x/tools/go/ssa`) de todo el
   paquete (incluidos tests). Encuentra **llamadas oráculo** (`isOracle`) y
   siembra un backward slice desde los **argumentos** de cada llamada. El slice
   recorre: data def-use, parámetros↔call-sites (interprocedural), free vars de
   closures, memoria gruesa (store/load por root), y **dependencia de control**
   (post-dominadores + fallback de dominadores). Toda línea que toca = "checked".
3. **Gap.** Por función cubierta: `covered − checked − structural = unchecked`.
   Imprime las líneas y `summary: N covered, M unchecked (X%)`.

### El límite (probado)

`isOracle` solo reconoce:

```go
func isOracle(pkgPath string) bool {
    return strings.Contains(pkgPath, "/testigo/assert") ||
        strings.Contains(pkgPath, "testify/assert") ||
        strings.Contains(pkgPath, "testify/require")
}
```

Los tests nativos usan `t.Errorf`/`t.Fatal` del paquete `testing` (no una lib de
asserts). Resultado real medido: `checkedcov $GOROOT/src/errors` →
**`86 covered, 86 unchecked (100%)`**. Inútil en stdlib y en la mayoría de repos
Go (que no usan testify). **Ese es el bloqueo que este plan elimina.**

---

## 2. El núcleo técnico: detectar "checked" en tests nativos

El problema: para `if got != want { t.Fatal("mismatch") }`, la llamada que falla
(`t.Fatal`) **no recibe** `got`/`want` como argumentos — el valor discriminante
está en la **condición del `if`** que la guarda. Sembrar desde los args (como hoy)
no captura nada.

**Observación clave:** el slicer YA computa dependencia de control. La condición
del `if` es la dependencia de control del bloque que contiene `t.Fatal`. Entonces
la extensión es:

> Para una llamada-oráculo nativa, sembrar el slice **no solo desde sus
> argumentos, sino desde las condiciones de las que su bloque depende por
> control** (los operandos del `if got != want`).

Esto reutiliza la maquinaria existente (`fnControlDeps`, post-dominadores) — la
condición `got != want` ya está en `controlDeps[block]`; solo hay que sembrarla
explícitamente para los fail-points nativos.

### Modelo de "oráculo" generalizado (pluggable)

Reemplazar `isOracle(pkgPath) bool` por un reconocedor que, dado un `*ssa.Call`,
devuelve sus **semillas**:

```go
type OracleHit struct {
    Args         []ssa.Value // valores pasados al oráculo (testify/testigo)
    SeedControl  bool        // además sembrar la condición que guarda el bloque
}

type Recognizer interface {
    // Recognize devuelve (hit, true) si la call es un punto de verificación.
    Recognize(call *ssa.Call) (OracleHit, bool)
}
```

Reconocedores:

| Recognizer | Reconoce | Semillas |
|---|---|---|
| `TestifyRecognizer` | funcs de `testify/assert`, `testify/require` | `Args` (saltando `t` y format strings) |
| `TestigoRecognizer` | calls a `/testigo/assert`, `.Called`, `.WithParams` | `Args` (lógica actual) |
| `NativeRecognizer` | métodos de `*testing.T/B`: `Error,Errorf,Fatal,Fatalf,Fail,FailNow` | `Args` **+ `SeedControl=true`** (operandos del `if` guardián) |

`NativeRecognizer` detecta el receptor por tipo (`testing.TB`/`*testing.T`) vía
`types.Info`, no por nombre de paquete. Cubre también `t.Helper`-wrapped asserts
si el helper termina llamando a `t.Fatal` (el slice interprocedural ya lo sigue).

### Casos que cubre / no cubre (declarar honesto)

- ✅ `if got != want { t.Errorf(...) }` (table-driven, idioma dominante)
- ✅ `if err != nil { t.Fatal(err) }`
- ✅ `if !reflect.DeepEqual(a,b) { t.Error(...) }`
- ✅ `assert.Equal(t, want, got)` (testify, ya casi funciona)
- ⚠️ asserts via helper propio (`checkEqual(t, got, want)`) — funciona si el
  slice interprocedural alcanza el `t.Fatal` interno (ya lo hace).
- ❌ aserciones "implícitas" sin comparación (ej. un test que solo corre código
  esperando que no paniquee) — no hay valor discriminante; se reporta como
  unchecked (correcto: no verifica outcome).
- ❌ golden-file / snapshot asserts que comparan bytes leídos de disco — semilla
  llega al read, no al valor lógico (limitación conocida, documentar).
- ❌ **round-trip de serialización** (`json.Encode(w/buf, v)` → `[]byte` →
  `Unmarshal(...,&out)` → assert `out.ID`). La línea del `Encode` queda flagueada
  unchecked aunque el body SÍ se asevera. Misma clase que golden-file. **Causa
  verificada** (fixture sin net/http, 2026-06-21): raw `buf.Write(b)`+`buf.Bytes()`
  SÍ conecta (checked); solo rompe el round-trip de `encoding/json`. Encode/Unmarshal
  escriben vía **reflection**, no son field-stores rastreables → la identidad lógica
  se pierde cruzando la frontera `[]byte`. No es net/http ni el buffer. Fix genérico
  imposible sin modelar la semántica de json (`Unmarshal(Marshal(v)) ≡ v` field-a-field);
  sería special-case de encoding/json (+gob/xml), no genérico.
  - Nota http: en `writeJSON` las líneas igual quedan checked por el path Code/status
    (`WriteHeader`, resuelto por dispatch acotado SUT+test); solo el `Encode` body-only
    (server.go:147) cae en este gap.

### Resolución de dispatch dinámico (interfaces / doubles) — implementado 2026-06-21

El slice moría en `StaticCallee()==nil` (interface invoke, func-value), ciego al
código alcanzado solo por dispatch (doubles inyectados, `http.ResponseWriter`).

- **Descartado: RTA whole-program** (`callgraph/rta`). Desde test entries alcanza
  `testing→reflect→runtime`; su modelado de reflection explota → colgó 120s en
  fixture mínimo, OOM en httpapi (pgx/testcontainers). Wrong tool.
- **Shipped: resolución acotada a scope SUT+test** (`resolveDynamicCallees`): junta
  los tipos concretos que el suite mete en interfaces (`*ssa.MakeInterface` en scope)
  y resuelve cada invoke vía `prog.MethodSets.MethodSet(t).Lookup` + `MethodValue`.
  Costo `|invokes|×|tipos|`, sin reflection, sin fixpoint. `callArg` maneja
  receiver-como-param0 y desenvuelve el boxing `MakeInterface`. Aplicado en
  `checkedcovssa` y `edgecovssa` (este último delega `checked` a checkedcov, así que
  el fix se comparte; solo reemplazó su RTA propio en `analyzeReachability`).
- Resultado httpapi: clase FP dominante `writeJSON` covered-unchecked **eliminada**
  (vía path Code). ~9-60s según deps, sin OOM.

### Siguiente frontera — estado persistente observado más tarde

Queda una clase FP importante que `strings` expone bien: **estado persistente
construido por helpers privados y observado después por una API pública**.

Caso representativo: `strings.Replacer`.

```
NewReplacer(oldnew...)
  -> (*Replacer).buildOnce
      -> (*Replacer).build
          -> makeGenericReplacer
              -> (*trieNode).add          // construye trie/tabla interna

test:
  r := NewReplacer(...)
  got := r.Replace(input)
  if got != want { t.Errorf(...) }
```

Semánticamente, los stores internos de `trieNode.add` y `makeGenericReplacer`
sí están verificados: si se rompe la tabla/trie, `Replace` devuelve otro string
y el test falla. Pero el slice actual no modela bien esa cadena:

```
constructor/helper privado -> objeto persistente retornado -> método público -> assert
```

El filtro shipped para **agregados locales retornados** baja mucho ruido en
funciones puras (`out := make(...); out[i] = ...; return out`) y evita reportar
buffers/slices temporales como unchecked. No alcanza para `Replacer`, porque el
estado se guarda en un objeto que sobrevive entre llamadas y se consume después.

#### Cómo atacarlo sin hardcodear `strings`

Modelar **heap summaries persistentes** por tipo/objeto:

1. Identificar funciones constructoras/mutadoras que escriben campos de un objeto
   que retorna o recibe por parámetro:
   - `return &T{...}`, `return t`, `r.field = ...`, `t.table[i] = ...`
   - resumir writes como `(type T, field/path) -> source positions`.
2. Identificar métodos públicos/observados que leen ese mismo estado y cuyo
   resultado/call-site está checked:
   - `func (r *T) Replace(...) string`
   - loads/calls que demandan `(type T, field/path)` y terminan en un assert.
3. Propagar checked hacia atrás:
   - si `(*T).Method` está checked y lee `T.path`,
   - y `constructor/helper` escribió `T.path`,
   - marcar esas writes como checked.
4. Mantener field-sensitivity:
   - si el test solo observa `Code`, no marcar `Header`.
   - esta es la protección contra tapar bugs reales como "campo no assertado".
5. Acotar el alcance:
   - solo paquetes SUT+tests, igual que `resolveDynamicCallees`;
   - depth de path bajo (`maxFieldPathDepth`);
   - no cruzar reflection/serialization.

#### Fixture mínimo para la próxima IA

Crear un test parecido a:

```go
type table struct {
    slots []string
}

func newTable(xs ...string) *table {
    t := &table{slots: make([]string, len(xs))}
    for i, x := range xs {
        t.slots[i] = x
    }
    return t
}

func (t *table) Join() string {
    return strings.Join(t.slots, ",")
}

func TestTable(t *testing.T) {
    got := newTable("a", "b").Join()
    if got != "a,b" {
        t.Fatalf("got %q", got)
    }
}
```

Esperado: los stores `t.slots[i] = x` deben quedar checked, pero un campo
persistente no leído por `Join` debe seguir unchecked. Ese segundo caso es
obligatorio para no perder precisión.

#### Estado actual medido en `strings`

Después de los filtros locales, `checkedcov` sigue reportando ruido relevante en
`replace.go.add`, `replace.go.buildOnce`, `makeGenericReplacer`, y parte de
`WriteString`/`Replace`. La mayoría no son gaps de test reales: son internals del
trie/buffer observados por `TestReplacer` a través de `Replace`/`WriteString`.

---

## 3. Arquitectura de la nueva CLI

### Módulo

Tool **standalone**, módulo Go propio, **sin dependencia de testigo**:

```
checkedcov/                      (repo/módulo nuevo: github.com/<org>/checkedcov)
  go.mod                         (deps: golang.org/x/tools)
  cmd/checkedcov/main.go         (CLI)
  internal/analysis/
    analyze.go                   (orquestación: load → ssa → slice → gap)
    recognizers.go               (Testify / Testigo / Native)
    slice.go                     (backward slicer — extraído de checkedcovssa)
    coverage.go                  (lector de -coverprofile)
    controldeps.go               (post-dominadores — extraído)
  internal/report/
    text.go                      (salida humana, como hoy)
    json.go                      (salida máquina para la IA)
```

La extracción es mayormente **mover + generalizar** `internal/checkedcovssa`
(que se queda en testigo como wrapper fino o se borra y testigo importa la lib).

### CLI

```
checkedcov [flags] <pkg-pattern>...     # ej: ./...  o  ./internal/auctions
  --oracles testify,native,testigo      # default: auto (todos)
  --format text|json                    # default: text
  --min-unchecked N                     # exit !=0 si algún pkg supera N% (gate CI)
  --include-tests                       # también analizar _test.go como SUT
  --json-out PATH                       # escribir JSON a archivo
```

Auto-detección de oráculos: si el paquete importa testify → activarlo; siempre
activar native. Esto hace que corra "out of the box" en stdlib y repos testify.

### Contrato de salida JSON (el input para la IA)

```json
{
  "package": "github.com/x/y/internal/auctions",
  "covered_lines": 123,
  "unchecked_lines": 6,
  "unchecked_pct": 5,
  "findings": [
    {
      "file": "service.go",
      "line": 222,
      "func": "Close",
      "statement": "ReserveMet: reserveMet,",
      "reason": "covered but no asserted value depends on it",
      "nearest_test_hint": "TestService_Close"     // best-effort
    }
  ]
}
```

`reason` y `statement` le dicen a la IA **qué** quedó sin verificar y **dónde**,
para que escriba el assert/caso faltante. `unchecked_pct` por función permite
priorizar.

---

## 4. Validación — medir que la señal sirve (precisión)

La señal "unchecked" debe ser **precisa** (no mandar a la IA a perseguir huecos
falsos). Se mide con un **oráculo de mutación a nivel statement** (operador
`any-covered-statement`, ya esbozado en `internal/srcmut`/`boundarymut`):

- Mutá un statement marcado **unchecked** → debe **sobrevivir** (TP: el hueco es
  real, ningún assert lo cazaba).
- Mutá un statement marcado **checked** → debe **morir** (TN: el assert lo caza).
- `precision = TP / (TP + FP)`, `recall = TP / (TP + FN)` sobre el corpus.

**Corpus para los números** (responde "¿cómo mejoramos los números?"): con el
soporte nativo, **stdlib entera + cualquier repo Go con tests** es corpus. Meta:
≥10 paquetes variados (stdlib `strings`, `strconv`, `net/url`, … + repos reales),
muy por encima del corpus actual de 1 proyecto. Reportar precision/recall por
oráculo (native vs testify) y por estilo de suite.

Reutilizar `internal/eval` (MAE/R²/Brier/AUC/precision) ya construido.

---

## 5. Fases

| Fase | Entregable |
|---|---|
| **0** | Extraer `checkedcovssa` → módulo `checkedcov` nuevo; CLI con paridad actual (text + `./...`); testigo importa la lib o mantiene su wrapper. Smoke en testigo-usage. |
| **1** | `Recognizer` pluggable; portar testify+testigo a recognizers (paridad). Tests unitarios sobre funciones pequeñas hand-verified. |
| **2** | **`NativeRecognizer`** + seeding por dependencia de control. Validar en `$GOROOT/src/errors`, `strconv`, `strings` → ya no 100%. |
| **3** | Salida JSON + flags (`--format json`, `--min-unchecked`, auto-detect oráculos). Contrato estable para la IA. |
| **4** | Validación por mutación: precision/recall de "unchecked" sobre corpus ≥10 pkgs (stdlib + testify). Reporte por oráculo. |
| **5** | Gate CI (`--min-unchecked`) + doc + ejemplos de consumo desde un loop de IA. |

---

## 6. Decisiones / abiertas

- **Nombre del módulo/binario**: `checkedcov` (provisorio). Definir org path.
- **¿testigo importa la lib o duplica?** Preferible: testigo importa
  `checkedcov/internal/analysis` vía API pública; el detector 19 del audit se
  vuelve un cliente. Evita dos copias del slicer.
- **Receptor `testing.TB` por tipo**: usar `types.Info` para reconocer
  `*testing.T`/`*testing.B`/`testing.TB`, no por nombre — robusto ante alias.
- **Helpers de assert propios**: confiar en el slice interprocedural; si falla,
  permitir `--assert-func pkg.Func` para marcar oráculos custom.
- **Límite honesto** (declarar en el README, estilo Just et al. §9.4): mide
  "valor llega a una comparación que puede fallar el test", no "el test es
  correcto". Snapshot/golden y aserciones sin comparación quedan fuera.

## 7. Lo que NO hacemos (ya existe)

- Mutación estándar (relacional/aritmética): usar `gremlins`. No rebuild.
- Loop LLM+feedback completo: existe (Qodo Cover-Agent, Mutahunter, AdverTest).
  `checkedcov` es **el proveedor de señal fina** que esos consumen, no el loop.
- El diferencial defendible = **checked-coverage como señal, nativa, sin doubles**
  (hueco real en el ecosistema Go) — no reimplementar lo de arriba.
