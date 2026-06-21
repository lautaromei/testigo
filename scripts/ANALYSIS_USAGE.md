# checkedcov + edgecov — guía para analizar paquetes Go

Dos analizadores estáticos (SSA) que miden **cobertura verificada**, no solo
cobertura de líneas. Responden lo que `go test -cover` y la mutación normal no:
*¿este código se ejecuta pero ningún assert depende de su resultado?*

- **checkedcov** — líneas cubiertas pero *unchecked*: se ejecutan pero ningún
  valor aseverado depende de ellas (backward slice desde los oráculos del test:
  `t.Error/Fatal` nativo, `testify`, `testigo`).
- **edgecov** — un nivel arriba: **edges** (llamadas), **branches** (lados de if),
  **effects** (stores/calls/sends) alcanzados-sin-verificar o no-alcanzados.

## Build

```sh
make build        # compila bin/checkedcov y bin/edgecov
```

## Correr en cualquier paquete

`PKG` acepta un **import path** (`strings`, `net/url`, `encoding/json`) o un
**directorio**. Los import paths se resuelven con `go list`, así que la **stdlib
funciona out-of-the-box** (GOROOT read-only no es problema: el coverprofile se
escribe a un temp, no al paquete).

```sh
make check   PKG=strings        # solo checkedcov
make edge    PKG=net/url         # solo edgecov
make analyze PKG=strconv         # ambos, texto

# JSON máquina-legible -> out/<pkg>.{checkedcov,edgecov}.json
CHECKEDCOV=bin/checkedcov EDGECOV=bin/edgecov scripts/analyze.sh strconv json
```

Binarios directos (equivalente):

```sh
bin/checkedcov "$(go list -f '{{.Dir}}' strings)"
bin/edgecov    --format json "$(go list -f '{{.Dir}}' net/url)"
```

Flags útiles:
- checkedcov: `--format text|json`, `--json-out PATH`, `--min-unchecked N` (gate CI).
- edgecov: `--format text|json|dot`, `--json-out PATH`, `--dot-out PATH`,
  `--coverprofile PATH` (usa un profile existente en vez de correr tests),
  `--project` (analiza todo bajo el dir con cobertura project-wide).

## Cómo leer la salida

### checkedcov
```
summary: 1081 covered statement-lines, 313 unchecked (29% run without feeding any asserted value)
```
Cada finding = línea cubierta cuyo valor no llega a ningún assert. `unchecked%`
alto = suite ejecuta mucho que no verifica.

### edgecov (3 niveles)
- `[1] effect-reached-unchecked` — efecto ejecutado, ningún assert depende → la
  señal firma (hueco de verificación a nivel efecto).
- `[2] branch-not-taken` — un lado del branch nunca se ejecutó (hueco de cobertura).
- `[3] effect-not-reached` — efecto en un path nunca ejecutado (hueco de orquestación).

## Qué detecta que la mutación NO

La mutación normal (gremlins: `<`→`<=`, `+`→`-`, negación de condicionales) muta
**cómputo de valor**. Es ciega a:
- **campos de output no aseverados acoplados a un path checked** — mutar el valor
  mata el mutante por el path verificado; no aísla que *ese campo* quedó sin chequear.
  checkedcov es field-sensitive y sí lo aísla.
- **branches/calls de error nunca ejercidos** — la mutación los marca NOT COVERED
  o ni genera mutante; edgecov los reporta como branch-not-taken / effect-not-reached.

## Gotchas (importante para interpretar)

1. **Necesita que `go test` del paquete pase** para obtener cobertura. Si los tests
   no compilan o requieren infra (DB, red, containers), la cobertura sale parcial o
   vacía y los findings se degradan. **Para la stdlib esto no aplica** — andan.
2. **Paquetes con dependencias pesadas** (pgx, testcontainers, docker) son lentos
   por el build SSA de todo el closure, pero **no revientan** (el dispatch dinámico
   se resuelve acotado a SUT+test, sin RTA whole-program). Esperá decenas de segundos.
3. **FP conocido — frontera de serialización**: `json.Encode(w, v)` →`[]byte`→
   `Unmarshal(&out)`→`assert out.X`. La línea del `Encode` sale unchecked aunque el
   body se asevere: encoding/json escribe vía **reflection**, no son stores
   rastreables. Misma clase que golden-file/snapshot. No es bug, es límite del slice.
4. **FP conocido — estado persistente observado después**: constructores/helpers
   privados que llenan un objeto y métodos públicos que lo observan más tarde.
   Ejemplo stdlib: `strings.NewReplacer` construye trie/tablas internas y
   `Replace`/`WriteString` se aseveran después. El slice actual aún no conecta
   bien `constructor -> objeto persistente -> método público -> assert`, así que
   `replace.go.add`/`makeGenericReplacer` pueden salir unchecked aunque
   `TestReplacer` valide el comportamiento público. Está documentado como próximo
   paso en `CHECKEDCOV_PLAN.md`.
5. **FN conocido — dispatch cross-stdlib**: routing de handlers vía `net/http`
   ServeHTTP no se traza (fuera de scope SUT+test). Interfaces *propias* del paquete
   (doubles inyectados, repos, notifiers) sí se resuelven.
6. **Ruido estructural** en checkedcov: tags de struct, `}`, `return` sueltos pueden
   aparecer; clase menor, ignorable.

## Targets stdlib sugeridos (livianos, buena señal)

`strings` · `strconv` · `bytes` · `net/url` · `path` · `path/filepath` ·
`encoding/csv` · `text/template` · `container/heap` · `math/bits`

Evitar de entrada los gigantes (`net/http`, `runtime`, `reflect`): andan pero lentos.
