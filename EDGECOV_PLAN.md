# Plan — `edgecov`: checked-edge coverage para orquestación (mutation barato, un tier arriba de `checkedcov`)

## 0. Objetivo

Construir una herramienta que detecte **fallos de interacción / orquestación**
que la suite no atrapa, sin correr mutation testing. La señal: un **diff entre
dos grafos** —lo que el código *podría* hacer vs. lo que los tests *hicieron y
verificaron*— a nivel de **arista de llamada, rama y efecto**, no de línea.

Es el tier que falta sobre `checkedcov`:

```
cobertura de línea          → "línea corrió"             go test -cover
checked-line cobertura      → "línea alimenta un assert" checkedcov (existe)
────────────────────────────────────────────────────────────────────────
checked-edge cobertura      → "la llamada/rama/efecto A→B corrió Y un assert lo mira"  ← edgecov
```

Salida pensada como **input para una IA que escribe tests**: no "esta línea no
está cubierta" sino "el `refund` que esta rama de error guarda nunca corrió" o
"se emite `OrderFailed` pero ningún assert lo verifica".

### 0.1 Por qué (la señal sobre la que nos paramos)

`checkedcov` resuelve el nivel **unitario**: bugs algorítmicos, data-flow intra
e inter-procedural hasta un assert. Pero a nivel **integración / e2e / wiring
entre componentes** es flojo: una suite puede ejecutar cada línea, tener asserts
útiles, y aun así no cubrir **casos de uso de orquestación** — la rama de error,
el evento que se emite en el camino sad, el rollback que limpia estado.

Esos son justo los fallos de interacción de la tesis (`mutants-not-substitute-
interaction-faults`): mutation los atrapa pero es caro; line coverage miente más
acá que en ningún otro lado. `edgecov` da el análogo **barato** — modelo
estructural + el slice de `checkedcov` ya validado, sin compilar/correr mutantes.

### 0.2 Por qué no más heurísticas

Las heurísticas fallan porque adivinan el patrón del bug. `edgecov` no adivina:
deriva la cobertura del **mismo data-flow** que ya probamos correcto a nivel
línea (`checkedcov-agreement-eval`: memdb 1.00). Misma señal, un tier arriba.

---

## 1. La idea: dos grafos + diff

**Static graph** — todo lo que el código *podría* hacer.
**Dynamic graph** — lo que los tests *efectivamente* hicieron (y verificaron).

```
Static edge not observed:
    service.CreateOrder → inventory.Reserve
    service.CreateOrder → payment.Refund
    service.CreateOrder → events.OrderFailed

Branch not observed:
    if paymentErr != nil
    if inventoryErr != nil
    if user.IsBlocked()

Effect not observed:
    emits OrderFailed
    deletes temporary reservation
    writes audit row
```

El diff crudo es sólido y barato. Pero tiene **dos trampas** (§5) sin las cuales
es solo branch coverage reempaquetado con un mar de falsos positivos.

---

## 2. Qué existe hoy (la base de la que partimos)

`internal/checkedcovssa/checkedcovssa.go` **ya tiene el motor**. El backward
slice **ya cruza fronteras de llamada**:

| Mecanismo | Dónde | Reusable para |
|---|---|---|
| `callSites[callee] []*ssa.Call` | L98, L116-119 | universo de aristas estáticas |
| `*ssa.Parameter` → args del caller | L314-330 | cruce de arista (entrada) |
| `*ssa.Call` return → returns del callee | L365-374 | cruce de arista (salida) |
| `argMutators` (slice/map escritos in-place) | L108, L127-131 | definición de **effect** |
| builtin `delete` como store | L121-125 | **effect** |
| `*ssa.Store` / `*ssa.MapUpdate` | L134-141 | **effect** |
| `fnControlDeps` (block → conds que lo guardan) | L566-635 | **branch → effect guardado** |
| `coveredLines` (cover profile, block `count`) | L770-802 | grafo **dinámico** barato |
| `recognize` (oráculos testify/native/testigo) | L741-760 | seeds del checked-slice |

Conclusión: **no construimos un motor nuevo.** Lift de la salida de
*conjunto-de-líneas* a *conjunto-de-aristas/effects*, más dos filtros nuevos.

---

## 3. El modelo

- **Nodo** = `*ssa.Function`.
- **Arista** = call-site `c` (`*ssa.Call`/`*ssa.Go`/`*ssa.Defer`), identificada por
  `(caller, callee, c.Pos())`. Para dispatch de interfaz el callee concreto puede
  ser múltiple (ver §8).
- **Branch** = bloque con `*ssa.If` terminal; las dos ramas son sus `Succs`.
- **Effect** = call que existe por su **efecto lateral**, no por su valor.
  Definición principista (sin hardcodear `*.Emit`/`*.Publish`):
  - return descartado (el `*ssa.Call` no tiene referrers), **o**
  - escribe por arg mutable (`argMutators`), `delete`, `*ssa.Store`,
    `*ssa.MapUpdate`, **o**
  - `*ssa.Send` a canal (extensión nueva — hoy no se trackea).

---

## 4. Las cuatro categorías × tres estados

La lista cruda tiene un estado por ítem. Faltan dos. Cada arista/branch/effect
vive en uno de:

```
not reached        → test nunca fue ahí           (gap dinámico)
reached, unchecked → test fue, no aseveró nada     (gap de oráculo)  ← LA JOYA
checked            → test fue y un assert lo mira   (ok, no se reporta)
```

`reached-unchecked` es lo único que branch coverage NO da. Es literal el mutante
de orquestación que **sobrevive**.

Ejemplo: test llama `CreateOrder`, payment falla, `OrderFailed` **sí se emite**
(reached), pero ningún assert chequea que se emitió → el mutante que borra el
emit sobrevive → `edgecov` lo marca `effect reached-unchecked`.

| Categoría | Estados posibles | Cómputo (de lo que YA tenés) |
|---|---|---|
| **Edge** | not-reached \| observed | `callSites` + reachability (§5.1); observed = block del call-site `count>0` |
| **Branch** | not-taken \| taken | cover profile ya da bloque `count==0` por rama |
| **Effect** | not-reached \| reached-unchecked \| checked | enumerar effects (§3); checked = ¿en backward slice de un oráculo? |

Branch coverage lo da Go gratis. **Arista** y **effect-checked** son lo único
nuevo — y los dos salen de máquina que ya escribimos.

---

## 5. Las dos trampas (make-or-break)

### 5.1 Filtro de reachability (si no, falsos positivos en masa)

El callgraph estático (CHA/RTA) **sobre-aproxima**: incluye aristas que no pueden
disparar. `static edge not observed` mezcla:
- gap real (falta test)
- arista infactible (dead code / artefacto de la aproximación)
- arista que solo dispara en e2e, no en este profile

**Fix v1:** no diffear `(todas las estáticas) − observadas` como finding. Usar
RTA desde roots `Test*` para contar aristas concretas alcanzables como métrica,
incluido dispatch de interfaz (`summary.interface_edges`), y dejar
`edge-not-observed` como diagnóstico. Arista inalcanzable ≠ gap; arista no
observada en línea no cubierta tampoco predice supervivencia útil.

Sin este filtro la IA se ahoga en ruido y deja de usar la tool.

### 5.2 Split reached vs checked (si no, es branch coverage)

Sin la capa `checked`, "effect not observed" = block coverage reempaquetado. El
valor está en distinguir `reached-unchecked` de `checked`, y eso sale del
backward slice de `checkedcov` (L279-404). Una arista/effect está `checked` ⟺
algún valor que fluye por ella está en el slice de un oráculo.

---

## 6. Ranking de salida (qué mostrar primero a la IA)

```
1. Effect reached-unchecked           ← mutante de orquestación VIVO. máxima señal.
2. Branch no-tomada que guarda effect ← falta test de error-path
3. Effect not reached                 ← falta el escenario entero
```

Este ranking **es** la contribución de diseño. Sin él los cuatro pesan igual y el
ruido entierra la señal.

**Branch → effect que guarda:** invertir `fnControlDeps` (L566). Dada una rama
no-tomada, qué effects domina su bloque controlado. Así el reporte no dice "rama
142 no cubierta" sino "el refund que esta rama guarda nunca corrió" — accionable.

---

## 7. Mapeo a operadores de mutation (falsación)

Cada categoría accionable predice qué mutante de orquestación sobrevive — **sin
correrlo**:

| Categoría edgecov | Mutante que predice vivo |
|---|---|
| Effect reached-unchecked | `DROP_CALL` / `DROP_EVENT` sobrevive |
| Branch no-tomada (guarda effect) | guard invertido/borrado no atrapado |

Criterio de falsación (estilo `audit-eval`): para cada categoría, ¿el mutante
correspondiente realmente sobrevive en la suite? Si `edgecov` marca
`reached-unchecked` y el `DROP_CALL` muere, el predicado es falso positivo →
calibrar. Validar contra mutation real con el harness existente (`audit-eval`),
igual que `checkedcov-agreement-eval`.

`REWIRE_CALLEE` falsó `edge-not-observed`: esa señal queda fuera de categorías
accionables. `SWAP_ORDER` y reordenamiento quedan **fuera de v1**: necesitan
modelo de secuenciación que hoy no existe. Documentar como límite (§12).

---

## 8. Grafo dinámico barato (cover profile primero, instrumentación después)

No hace falta instrumentar para el 80%:

- **SSA/RTA concrete call edges + branches + effect-reached** → salen del cover profile
  que `coveredLines` (L770) ya carga. Block del call-site `count>0` ⇒ arista
  observada. Para interfaz, RTA aporta targets concretos aproximados.
- **Runtime dispatch instrumentation** queda fuera de v1. Solo se justifica si
  RTA sobre-aproxima demasiado en corpus reales.

**Extensión integración/e2e** (el punto de "alto nivel"): hoy `coveredLines`
corre `go test -coverprofile` en un paquete. Para e2e: binarios `go build -cover`
+ `GOCOVERDIR` agregado (`go tool covdata textfmt`) → alimenta el mismo mapa
covered-lines. Así checked-edge cubre flujos e2e, no solo unit. Fase 3.

---

## 9. Plan por fases

Leyenda: ✅ hecho · ⚠️ parcial · ❌ falta

### Fase 0 — Medir antes de construir ✅
Baseline sobre memdb cubierto por `edgecovssa_test.go` (effects + reached-unchecked
no-cero). No se escribió volcado separado; el test fija el número base.

### Fase 1 — Edge + branch coverage estructural ✅
1. ✅ Aristas desde `callSites`, `observed` por cover profile.
2. ✅ Reachability filter con **`rta`** desde roots `Test*` (`analyzeReachability`).
3. ✅ Branches del cover profile (`collectFunc`, lado `count==0`).
4. ✅ Aristas concretas quedan en métricas (`edges`, `interface_edges`,
   `edges_unobserved`). `edge-not-observed` fue falsado y ya no se emite como
   finding accionable.

### Fase 2 — Checked split ✅
1. ✅ Effects: `callEffect` (return-descartado / arg mutable / delete) + `*ssa.Store`
   + `*ssa.MapUpdate` + `*ssa.Send`.
2. ✅ `checked` vía `checkedLinesForTargets` (reusa `checkedcovssa.IsCheckedFile`;
   el slice de checkedcov ahora es field-sensitive para stores por parámetro/campo).
3. ✅ Clasificación `not-reached` / `reached-unchecked` / `checked`.
4. ✅ Branch → effect guardado (`controlledEffects`).
5. ✅ Salida accionable rankeada + JSON + DOT; aristas quedan como métricas.

### Fase 3 — Integración/e2e + dispatch dinámico ✅
- ✅ `-coverprofile=...` externo (e2e / `GOCOVERDIR` convertido) en `coveredLines`.
- ✅ Modo `-project` (cobertura agregada `./...`) — **necesario**: per-package los
  paquetes de orquestación colapsan a 0 effects (callees en otros pkgs / mockeados).
- ✅ Dispatch concreto por RTA contado en `summary.interface_edges`; `edgecov` ya
  emite aristas concretas para `invoke`/interface calls (y `go`/`defer`). No es
  instrumentación runtime, pero queda medible y testeado.
- ⏭️ Instrumentación runtime de call-site para target concreto de interfaz queda
  fuera de v1. Solo hace falta si RTA sobre-aproxima en corpus reales.

### Fase 4 — Calibración / falsación ✅
- ✅ Operadores: `DROP_CALL`, `DROP_EVENT` y `REWIRE_CALLEE` implementados en
  `boundarymut` para medir effect-drop y wrong-target routing.
- ✅ `cmd/edgecov-eval`: agreement edgecov ⟷ supervivencia de mutantes reales,
  con TP/FP/TN/FN, precision/recall/accuracy, `-debug` per-línea, crash-kills y
  modo `-project`.
- ✅ Falsación ejecutada: `effect-reached-unchecked` tiene señal real pero FP por
  heap-boundary; `edge-not-observed` quedó descartado como predictor accionable.

---

## 9.1 Resultados de falsación (medido, no asumido)

`edgecov-eval` (predicción `effect-reached-unchecked` vs supervivencia real de
DROP_CALL de `boundarymut`).

| Corpus | sites | TP | FP | TN | FN | prec | rec |
|---|--:|--:|--:|--:|--:|--:|--:|
| testigo-usage `internal/*` (6 pkgs) | 10 | 0 | 0 | 10 | 0 | — | — |
| **cmd/api/httpapi** (target de `-project`) | 15 | 2 | 3 | 7 | 3 | 0.40 | 0.40 |
| stdlib container/list | 7 | 0 | 5 | 2 | 0 | 0.00 | — |
| stdlib net/url | 42 | 2 | 25 | 14 | 1 | 0.07 | 0.67 |

**Lo que funciona** — edgecov encuentra huecos reales que la suite no cubre:
- httpapi `dec.DisallowUnknownFields()` (TP): ningún test manda campo desconocido.
- httpapi `w.Header().Set("Content-Type")` (TP): ningún test asevera el header.

**Clase FP dominante (heap-boundary)** — call que escribe a estado mutable
(`strings.Builder.WriteString`, `list.move`, `w.WriteHeader`, `writeError`)
validado leyendo ese estado después (`buf.String()`, `rec.Code`, traversal). El
DROP_CALL muere (el assert final lo atrapa) pero edgecov lo marca
reached-unchecked. Causa: el checked-slice es **línea-base, no modela aliasing de
heap** — gap **compartido con checkedcov** (`checkedcov` imprime las mismas líneas
`covered, unchecked`). edgecov hereda fiel el slice → hereda el FP.

**Fix receiver-flow → RECHAZADO por medición.** Marcar effect `r.M(...)` checked si
el receiver `r` fluye a un oráculo: en httpapi mata los 3 FP pero **también los 2
TP** — `w` está checked para *status* (`rec.Code`) pero no para *Content-Type
header*; mismo receiver, distinto campo. Heurística receiver/línea no distingue
granularidad de campo → destruye la señal real. Fix correcto = **checked
field-sensitive** (caro, trabajo en `checkedcovssa`).

**Límite de operador → RESUELTO con `DROP_EVENT`.** Los `writeJSON(w,status,v)`
que edgecov marca reached-unchecked eran **NOT_VIABLE** para DROP_CALL: dropear
deja `v` sin uso → error de compilación. `DROP_EVENT` (implementado en
`boundarymut`: reemplaza el call-stmt por `_, _, _ = <args>` — consume los args
vía blanks, dropea el effect, queda compilable) los vuelve viables.

**Medición DROP_EVENT (suite testigo-usage completa, 11 pkgs, 37 sites).**
edgecov solo predice en **httpapi** (12/12); resto de paquetes 0 predicciones
pese a tener sites → per-package no ve orquestación cross-package (necesita
`-project`, §13.1 paso 3). Los 8 sites writeJSON/writeError/WriteHeader, ahora
medibles, salen **todos KILLED** (la suite atrapa el drop del body vía asserts de
response) → **8 FP**. Corpus: **precision 0.18, recall 0.29** (TP=2 FP=9 TN=21
FN=5). Los 2 TP reales: `DisallowUnknownFields` y `Header().Set` (Content-Type no
aseverado). Conclusión: DROP_EVENT confirma que writeJSON-style **es** la clase FP
heap-boundary, no señal real. El operador hizo su trabajo: hizo medible el
positivo core indroppeable y lo falsó.

**Veredicto.** `effect-reached-unchecked` da señal real (precision ~0.40 en
httpapi) pero ruidosa por la clase FP heap-boundary. No es FP-shippable como
predictor exacto sin checked field-sensitive. El gap inmediato es de **operador**
(DROP_EVENT/REWIRE_CALLEE), no de heurística.

Notas de harness:
- El eval debe correr sobre el target real (`-project` o `cmd/api/httpapi`); per-package
  en orquestación da 0 effects.
- stdlib read-only: copiar a temp + `go.mod` (boundarymut muta en sitio; `go test`
  read-only falla). `edgecov` plano sí corre sobre GOROOT (coverprofile va a TempDir).

---

## 9.2 Falsación de `edge-not-observed` con `REWIRE_CALLEE` (medido)

`REWIRE_CALLEE` implementado para falsar la predicción "edge-not-observed ⟹ REWIRE
vivo". Medido en `cmd/api/httpapi` (`-project`): **REWIRE 7 LIVED, 2 NOT_VIABLE,
0 KILLED**. Los 7 LIVED son `server.go:26-32` — registros de ruta `mux.HandleFunc`
adyacentes con firma idéntica; rewirearlos (intercambiar qué handler atiende qué
ruta) **no lo atrapa ningún test** → gap de orquestación real.

**Veredicto: `edge-not-observed` es predictor inútil de supervivencia REWIRE.**
Dos fallas estructurales, ambas medidas:

1. **edge-not-observed ⟺ línea no cubierta.** `edgecovssa` fija
   `observed = cov.covered(file,line)` (L371). Toda arista no-observada vive en
   línea con `count==0`; ahí `boundarymut` marca `NotCovered` y **no corre el
   mutante**. La "supervivencia" es vacua (nada lo ejecuta) — exactamente line
   coverage reempaquetado, más ruido RTA.

2. **Los REWIRE que de verdad sobreviven son aristas OBSERVADAS que edgecov filtra.**
   Las 7 supervivencias (HandleFunc routing) están en líneas cubiertas → edgecov
   las clasifica `observed` → **no las marca**. edgecov predice el conjunto
   equivocado: ignora justo donde el rewire es letal y no atrapado.

Además el ranking-4 es masivamente ruidoso: 265 findings en testigo-usage,
dominados por sobre-aproximación RTA (decenas de targets `.Error()` infactibles
—archive/tar, crypto/aes…— en un solo `server.go:138`). Confirma §12 (RTA
sobre-aproxima) y §6 (edge-not-observed va último).

**Conclusión.** `REWIRE_CALLEE` hizo su trabajo de falsación: enterró
`edge-not-observed` como categoría accionable. La señal genuina de rewire
(handler de ruta intercambiado) exige modelar **aristas observadas con dispatch
concreto** (Fase 3), no el diff estático−observado actual. `edge-not-observed`
no es shippable.

---

## 10. Contrato de salida

JSON (máquina / IA) + DOT (humano). Borrador:

```json
{
  "package": "…/service",
  "summary": { "edges": 0, "edges_unobserved": 0, "interface_edges": 0,
               "branches": 0, "branches_untaken": 0,
               "effects": 0, "effects_reached_unchecked": 0, "effects_unreached": 0 },
  "findings": [
    { "rank": 1, "kind": "effect-reached-unchecked",
      "func": "CreateOrder", "file": "service.go", "line": 88,
      "effect": "emits OrderFailed",
      "guarded_by": "if paymentErr != nil",
      "predicts": "DROP_EVENT survives",
      "reason": "effect ejecutado pero ningún assert depende de él" }
  ]
}
```

Reusar `Finding`/`Report` de `checkedcovssa` como modelo; nuevo paquete
`internal/edgecovssa` + `cmd/edgecov` (paralelo a `checkedcov`).

---

## 11. Validación

- **memdb** primero (baseline 1.00 conocido).
- Agreement `edgecov` ⟷ mutation de orquestación vía `audit-eval`/`edgecov-eval`.
- Métrica: precisión de `effect-reached-unchecked` como predictor de mutante
  vivo (no asumida — medida).

---

## 12. Límites honestos (declarados de entrada)

- **Path explosion:** cobertura de *todos* los caminos interprocedurales es
  exponencial. v1 hace **aristas + ramas + effects**, no caminos completos. No
  prometer "toda rama del árbol".
- **Reachability sobre-aproxima:** RTA marca alcanzable de más. Por eso
  `edge-not-observed` queda solo como métrica diagnóstica, no como finding.
- **Sin orden:** `SWAP_ORDER`/reordenamiento de eventos fuera de v1.
- **Effects vía estructura, no semántica:** "escribe audit row" se infiere de
  efecto lateral (store/send/return-descartado), no de entender qué hace. Puede
  marcar effects irrelevantes; calibrar con `audit-eval`.
- **Dispatch de interfaz:** v1 usa targets concretos de RTA y expone
  `summary.interface_edges`; la instrumentación runtime queda fuera de v1.

---

## 13. Preguntas abiertas

1. ✅ **DECIDIDO**: paquete separado `internal/edgecovssa` + CLI propio `cmd/edgecov`.
2. ✅ Granularidad de "effect" — hoy **todo side-effect**. La medición (§9.1)
   muestra que la clase FP dominante (writes a accumulator mutable) NO se filtra
   por "cross-component" (Builder/list son otros paquetes). El discriminador real
   es **checked field-sensitive**, no la granularidad de effect.
3. ✅ `rta` vs `vta`: v1 queda en **`rta`**. `vta` no se incorpora hasta que una
   medición de corpus justifique cambiar el coste/precisión.

## 13.1 Próximos pasos (orden recomendado, post-medición)

1. ✅ **`DROP_EVENT` en `boundarymut`** — HECHO. Reemplaza call-stmt por
   `_, _, _ = <args>` (consume args, dropea effect, compila). edgecov-eval acepta
   DROP_CALL+DROP_EVENT con dedup per-site (viable gana). Midió el positivo core
   writeJSON-style → 8 FP, precision 0.18 corpus (§9.1). Falsó la señal core.
2. ✅ **`REWIRE_CALLEE` en `boundarymut`** — HECHO. Swap del `Fun` de dos calls
   adyacentes (args fijos): redirige arista A→t1 a A→t2; firma incompatible →
   NOT_VIABLE (mismo path que DROP_CALL). Falsó `edge-not-observed` → **predictor
   inútil** (ver §9.2). El operador en cambio descubrió señal real que edgecov NO
   marca (rewire de handler de ruta).
3. ✅ **edgecov-eval en modo `-project`** — HECHO. `boundarymut.Options.ProjectRoot`
   juzga cada mutante contra `go test ./...` del módulo (coverage `-coverpkg=./...`);
   eval `-project` toma predicciones de `AnalyzeProject` y keya por path absoluto.
   Medido (testigo-usage, 17 dirs, 63 sites): **prec 0.18 rec 0.25 acc 0.76**
   (TP=2 FP=9 TN=46 FN=6). edgecov **sigue prediciendo solo en httpapi (12/12)** con
   coverage project-wide → la señal es genuinamente HTTP-boundary, no artefacto de
   per-package. httpapi idéntico a per-package (los tests propios ya matan los FP
   writeJSON). Los +26 sites nuevos son todos TN (e2e + helpers). 2 FN en
   storage/postgres = mutantes que ningún test mata y edgecov no predice (hueco
   ciego). Conclusión: el scope de test NO es el discriminador; lo es checked
   field-sensitive (paso 4).
4. ✅ **Checked field-sensitive** (en `checkedcovssa`) — HECHO. Resume stores por
   parámetro/campo, propaga efectos por calls directos y el slice demanda campos
   concretos (`rec.Code` no marca `rec.HeaderMap`). Test de regresión en
   `internal/checkedcovssa`: helper que escribe `Code` queda checked; helper que
   escribe `Header` sigue unchecked.
5. ✅ **Dispatch concreto de interfaz en `edgecovssa`** — HECHO en el modelo RTA:
   `summary.interface_edges`, aristas `invoke` concretas y `edge-not-observed`
   fuera de findings accionables. Runtime instrumentation queda como línea futura
   solo si RTA sobre-aproxima en corpus reales.

## 13.2 Iteración siguiente (post-v1, validación testigo-usage verde)

Baseline corregido en `testigo-usage`: `internal/auctions.Service.Create` valida
`products.Get(ctx, productID)` antes de `FindOverlapping`; `go test ./...` queda
verde.

Medición project-wide limpia:

```text
edgecov -project:
  edges=792
  interface_edges=647
  edges_unobserved=269   # métrica diagnóstica, no finding
  effects=70
  effects_reached_unchecked=12
  effects_unreached=23

edgecov-eval -project:
  63 sites
  precision=0.18 recall=0.25 accuracy=0.76
  TP=2 FP=9 TN=46 FN=6
```

**Veredicto v1.** La limpieza de `edge-not-observed` funcionó: ya no contamina
findings. Pero `effect-reached-unchecked` sigue flojo como predictor exacto:
encuentra señal real (2 TP), pero arrastra 9 FP y deja 6 FN.

Orden recomendado para la próxima iteración:

1. **Dump `-debug` y clasificar FP/FN línea por línea.**

   ```bash
   go run ./cmd/edgecov-eval -project -debug -timeout 1m /Users/lautaromei/git/testigo-usage
   ```

   Objetivo: tabla cerrada de las 15 líneas problemáticas reales:
   - FP = edgecov predice vivo, mutante muere.
   - FN = edgecov no predice, mutante vive.

2. **Atacar primero los 9 FP de `cmd/api/httpapi`.** El reporte actual apunta a
   response-boundary:
   - `writeJSON(...)`
   - `writeError(...)`
   - `w.WriteHeader(status)`
   - `json.NewEncoder(w).Encode(value)`

   Hipótesis: checked field-sensitive aún no conecta bien asserts sobre
   `httptest.ResponseRecorder` con efectos transitivos sobre `http.ResponseWriter`.
   Hay que modelar campos lógicos de response:
   - `ResponseWriter.WriteHeader` → `status`
   - `ResponseWriter.Write` / `json.Encoder.Encode` → `body`
   - `Header().Set("Content-Type")` → `header.Content-Type`

   Regla de oro: no marcar todo `w` como checked. `rec.Code` puede comprobar
   status y `rec.Body` puede comprobar body, pero eso no debe comprobar
   `Content-Type`.

3. **Agregar resumen field-sensitive de heap-boundary indirecto.** El modelo
   actual resume stores SSA por parámetro/campo. Falta un resumen de "este call
   muta el campo lógico X del argumento Y" para APIs boundary:
   - `json.NewEncoder(w).Encode(value)` deriva writer y termina escribiendo body.
   - `w.WriteHeader(status)` escribe status.
   - `w.Header().Set(k, v)` escribe header específico.

4. **Después atacar los 6 FN.** Preguntas por cada FN:
   - ¿el site no fue reconocido como effect?
   - ¿coverage project no lo marcó reached?
   - ¿checkedcov lo marcó checked por sobreaproximación?
   - ¿el mutante vive por equivalente/semántica no observable?

5. **Criterio de avance.** Próxima meta: subir precisión antes que recall.

   ```text
   objetivo: precision > 0.50 manteniendo recall >= 0.25
   ```

   No optimizar recall todavía; primero bajar FP para que el reporte sea
   accionable.

## 13.3 Caso fuera de mutation local: overwrite temporal multi-writer

Fixture agregado en `testigo-usage/internal/cronoverwrite`:

- `ManualPost(user, value)` escribe una preferencia manual.
- `CronRefresh(user, value)` escribe una preferencia importada por cron.
- Los tests cubren ambos writers por separado y pasan.
- El test saltado `TestCronRefreshDoesNotOverwriteManualPreference` documenta el
  bug real: si el usuario hace POST manual y después corre el cron, el cron pisa
  el valor manual.

Este caso representa una clase que mutation local no ataca bien aunque todo esté
"testeado" por unidad: el problema no es dropear un write, sino una **invariante
temporal entre writers** sobre la misma entidad/campo. El detector necesario no
es `effect-reached-unchecked`, sino algo como:

```text
same storage key/field written by multiple workflows
AND no test cubre/asevera el orden o la política de precedencia
```

Observación inicial:

```text
edgecov internal/cronoverwrite:
  effects=1
  effects_reached_unchecked=1  # map update Store.Set

edgecov-eval internal/cronoverwrite:
  0 mutation sites viables para el bug temporal
```

Conclusión: agregar una línea futura separada, no mezclarla con el predictor
actual. Nombre tentativo: `state-precedence-gap` o `multi-writer-invariant-gap`.
