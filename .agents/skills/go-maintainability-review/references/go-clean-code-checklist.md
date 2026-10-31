# Go Maintainability & Clean Code Checklist (SelfCloud)

Use this reference during **Phase 2 (Semantic Inspection)** and **Phase 3 (Filtering & Refactoring)** of the `go-maintainability-review` skill.

---

## 1. Reducing Copy-Paste & Duplication (DRY vs. Premature Abstraction)

### High-Value Deduplication Targets
1. **Repository Query Helpers (`internal/repository/`)**:
   - **Pattern**: Multiple repository methods (`GetByUsername` vs `GetByID`, `GetTokenByAccessToken` vs `GetTokenByRefreshToken`, `DeleteByUserID` across repos) that build the exact same `Select(...)`, execute `QueryRowContext`, `Scan(...)` the same struct fields, and check `errors.Is(err, sql.ErrNoRows)`.
   - **Fix**: Extract an unexported receiver helper on the repository struct, e.g.:
     ```go
     func (r *userRepo) getUserWhere(ctx context.Context, where goqu.Ex) (*domain.User, error)
     ```
2. **Duplicated Handler Utilities (`internal/api/`, `internal/webdav/`)**:
   - **Pattern**: Repeated request-inspection helpers copy-pasted across handlers (e.g., base URL resolution checking `X-Forwarded-Proto` / `r.TLS` / `localhost:8080` in `graph_handler.go`, `oauth_handler.go`, `shares_handler.go`; or identical JSON/XML error response + audit logging blocks).
   - **Fix**: Consolidate shared handler helpers into a package-level helper file (e.g., `internal/api/helpers.go` or `internal/httputil/`) when the logic is identical in purpose and behavior.
3. **Repeated Precondition / Validation Blocks**:
   - **Pattern**: Copy-pasted path normalization, user extraction from context, or node existence checks within the same handler file.
   - **Fix**: Extract small unexported helper methods on the handler struct.

### When NOT to Deduplicate (False-Positive Guardrails)
- **Divergent Domain Semantics**: Do not merge two functions just because they look structurally similar if they represent independent domain concepts that will evolve separately (*"A little copying is better than a little dependency"*).
- **Trivial SQL Literals**: Do not replace clear `goqu` column literals (`goqu.Ex{"user_id": id, "path": p}`) with global string constants (`colUserID`, `colPath`) unless a complex expression is repeated verbatim.

---

## 2. Reducing Cyclomatic & Cognitive Complexity

Target functions where `gocyclo > 15`, `gocognit > 20`, `nestif >= 4`, or `funlen > 80 lines / 50 statements`.

### Refactoring Techniques
1. **Flatten Arrow Code with Guard Clauses (Early Returns)**:
   - Invert `if condition { ... 40 lines ... }` to `if !condition { return ... }` so the happy path stays at indentation level 1.
   - Replace `if err != nil { ... } else { ... }` with an early return inside the `if err != nil` block (`revive: early-return`, `indent-error-flow`).
2. **Decompose Monolithic HTTP & WebDAV Handlers**:
   - Split 100+ line handlers (like `UploadHandler`, `GoogleCallbackHandler`, `handleAuthorizePost`, `handlePut`, `handleCopyDir`) into cohesive phases:
     1. Parse & validate request input.
     2. Execute domain/storage/repository operations via focused helper methods.
     3. Record `[AUDIT]` event and write HTTP response.
3. **Decompose Large CLI Subcommand Dispatchers (`cmd/selfcloud/main.go`)**:
   - When a CLI dispatcher like `cmdUser` or `cmdServe` exceeds 50+ cyclomatic complexity due to handling many subcommands (`create`, `list`, `delete`, `update`, `set-password`, `set-email`) inline, extract each subcommand into its own dedicated function (`cmdUserCreate`, `cmdUserList`, etc.) and keep `cmdUser` as a thin router.
4. **Decompose Auth & Middleware Pipelines (`internal/middleware/`)**:
   - In complex middleware like `DualAuthMiddleware`, extract authentication strategies into dedicated helpers (e.g., `authenticateBearer(...)`, `authenticateBasic(...)`) rather than deeply nesting both flows in a single closure.
5. **Replace Long `if-else` Chains with `switch`**:
   - Use tagged or tagless `switch` statements when branching on the same variable or status category (`gocritic: ifElseChain`).

---

## 3. Go 1.27 & Clean Code Practices

1. **Error Handling**:
   - Always use `errors.New("...")` instead of `fmt.Errorf("...")` when there are no format verbs (`perfsprint: error-format`).
   - Always wrap underlying errors with `%w` (`fmt.Errorf("failed to ...: %w", err)`) and inspect errors using `errors.Is` / `errors.As` (`errorlint`).
   - Never call `os.Exit` / `log.Fatal` inside a function after registering `defer sqlDB.Close()` (`gocritic: exitAfterDefer`); instead, return an `error` to `main()` or a wrapper function so deferred cleanup runs before exiting.
2. **Unused Parameters & Receivers (`unparam`, `revive`)**:
   - Remove genuinely unused parameters from internal helper methods (e.g., unused `basePath` or `sourceNode` in internal `handleMove`/`handleCopy`/`handleCopyDir` helpers, or constant-only parameters like `generateSecureToken(32)`).
   - **Exception**: Keep `w http.ResponseWriter, r *http.Request` on standard `http.HandlerFunc` methods even if `r` is unused; you may rename `r` to `_` only if it improves clarity without hurting readability.
3. **Avoid Package Import Shadowing (`gocritic: importShadow`)**:
   - Rename local variables or parameters named `storage` (e.g., to `store` or `blobStore`) when package `selfcloud/internal/storage` is imported in the same file.
4. **Modern Go Standard Library Idioms**:
   - Prefer `strconv.Itoa(n)` over `fmt.Sprintf("%d", n)`.
   - Prefer `%q` in `fmt.Sprintf` over `"\"%s\""` (`gocritic: sprintfQuotedString`).
   - Prefer `0o755` / `0o644` octal literals over legacy `0755` (`gocritic: octalLiteral`).
   - Prefer `http.NoBody` over `nil` in `http.NewRequestWithContext` when sending no body (`gocritic: httpNoBody`).
   - Prefer `strings.Cut`, `strings.CutPrefix`, `strings.CutSuffix`, `slices.Contains`, `errors.AsType[T]`, `new(expr)`, `min()`, and `max()` over manual index slicing, temporary pointer variables, or verbose loops.

---

## 4. SelfCloud Non-Negotiable Invariants (`AGENTS.md`)

When proposing or applying any maintainability refactoring, **never** violate these project rules:
1. **Audit & Logging Order**: Always preserve `audit.Record(ctx, ...)` calls before HTTP responses/access logs, and keep `slog` context correlation (`request_id`, `int_request_id`).
2. **Database Node Invariants**: Any refactoring touching node updates or moves must still update all four fields: `name`, `path`, `parent_path`, and `updated_at`, using `goqu`.
3. **Security Boundaries**: Never weaken or bypass `checkSymlinkEscape`, `pathutil.ValidatePathLength` (max 700 chars), CSRF origin validation, or `TRUST_PROXY` IP resolution.
4. **WebDAV & OCS Compliance**: Preserve exact HTTP status codes, headers (`ETag`, `DAV`, `Retry-After`, `WWW-Authenticate`), and XML response structures required by ownCloud clients.
