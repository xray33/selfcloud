# SelfCloud Go Engineering & Maintainability Standards

All AI agents and contributors writing or modifying Go code in SelfCloud (`cmd/...`, `internal/...`, `test/...`) MUST adhere to these engineering standards. These rules are enforced automatically by `golangci-lint` via `make lint` and `make ci`.

---

## 1. Complexity & Function Size Limits

Every non-test Go function must stay within these hard limits:

| Metric | Linter | Maximum Threshold | Required Action When Approaching Limit |
| :--- | :---: | :---: | :--- |
| **Cognitive Complexity** | `gocognit` | **20** | Extract sub-steps, loop bodies, or validation blocks into named helper functions. |
| **Cyclomatic Complexity** | `gocyclo` | **15** | Decompose `switch`/branching dispatchers into per-case handler functions. |
| **Function Length** | `funlen` | **80 lines / 50 stmts** | Split setup, I/O execution, transaction persistence, and response rendering. |
| **Nesting Depth** | `nestif` | **4 levels** | Invert conditions into early-return guard clauses (`if err != nil { return err }`). |

### Required Structural Patterns
1. **Guard Clauses Over `else` Chains**: Never write `if cond { ... return } else { ... }`. Return or `continue` early so the happy path remains at the top indentation level.
2. **Three-Phase HTTP / WebDAV Handlers**:
   - **Phase 1 (Validate)**: Parse request inputs, validate path length (`pathutil.ValidatePathLength`), and check preconditions.
   - **Phase 2 (Execute)**: Perform filesystem I/O outside the DB transaction where possible, then persist metadata atomically via a focused helper (`persist*`, `save*`, `deleteNodeMetadata`).
   - **Phase 3 (Respond)**: Record the audit event (`audit.Record` / `audit.RecordFailure`) and write the HTTP response.
3. **Context Structs for Recursive / Multi-Step Operations**: When recursive directory walks or reconciliation loops require more than 4 arguments, group shared dependencies (`ctx`, `user`, repositories, state maps) into a small unexported struct (e.g., `dirCopyContext`, `orphanPruner`) with small methods (`walk`, `copyChild`).
4. **No Inline Large Templates or Migrations**: Keep HTML/CSS templates and multi-statement SQL schemas in package-level `const` declarations or dedicated per-version migration helpers (`migrateV1`, `migrateV2`, etc.).

---

## 2. Zero Duplication & Constant Hygiene

1. **No Copy-Paste Blocks (`dupl` threshold: 80 tokens)**:
   - In `internal/repository`, use shared query/mutation helpers (`getUserWhere`, `getTokenWhere`, `deleteTokensWhere`, `deleteSessionsWhere`) for single-row `goqu` lookups and conditional deletes.
   - In `internal/api`, use shared helpers in `internal/api/helpers.go` (`writeJSON`, `writeJSONError`, `writeStatusOK`, `resolveBaseURL`).
   - In `internal/middleware`, share session token validation (`resolveSessionUser`) across cookie and Bearer authentication flows.
2. **Named Constants for Repeated Literals (`goconst`: max 3 occurrences of strings $\ge 4$ chars)**:
   - Use package-level column constants (`colUserID`, `colPath`, `colUpdatedAt`, `colToken` in `internal/repository/repository.go`) for repeated SQL column names.
   - Define protocol and header constants (`schemeHTTP`, `schemeHTTPS`) rather than repeating magic strings.

---

## 3. Go 1.27 Idioms & Clean Code Rules

1. **Error Construction & Inspection (`errorlint`, `revive`, `gocritic`)**:
   - Use `errors.New("static message")` when no formatting verbs are needed (never `fmt.Errorf("static message")`).
   - Always wrap errors with `%w` (`fmt.Errorf("context: %w", err)`).
   - Always inspect errors with `errors.Is(err, target)` or `errors.As(err, &target)` (never `err == sql.ErrNoRows` or direct type assertions).
   - Keep error strings lowercase without trailing punctuation.
2. **String Formatting (`perfsprint`, `gocritic`)**:
   - Use `%q` in `fmt.Sprintf` instead of manual escaped quotes `\"%s\"`.
3. **Octal File Permissions (`gocritic`)**:
   - Always use Go 1.27 octal literal syntax `0o755`, `0o644`, `0o600` (never legacy `0755`).
4. **Unused Parameters, Receivers & Shadowing (`unparam`, `revive`, ` gocritic`)**:
   - Remove unused parameters from internal functions. If a signature is fixed by an interface or `http.HandlerFunc`, rename the unused parameter to `_`.
   - If a method does not access struct fields, omit the receiver variable name (`func (*Handler) helper(...)`).
   - Never shadow imported package names (`storage`, `path`, `url`, `context`, `db`) with local variable names.
5. **Resource Cleanup & `defer` Safety**:
   - Never call `os.Exit` inside a function that registers `defer` cleanup statements (such as `defer sqlDB.Close()`). Return an `error` to `main()` and let `main()` exit after all `defer` statements execute.

---

## 4. Mandatory Verification Before Completing Any Task

Whenever you add or modify Go code, you MUST run:
```bash
make lint && make test
```
Or for full-stack / pre-commit verification:
```bash
make ci
```
If any maintainability linter (`gocognit`, `gocyclo`, `funlen`, `nestif`, `dupl`, `goconst`, `gocritic`, `revive`, `errorlint`, `unparam`, `perfsprint`) reports an issue, refactor the code immediately—never add `//nolint` directives unless there is an unavoidable external interface constraint.
